package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// registrationBackoff is the retry ladder used while registering the webhook.
// A tunnel or reverse proxy in front of SchedLock may not be reachable at the
// moment the process starts, so registration is retried rather than abandoned.
var registrationBackoff = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	30 * time.Second,
}

// RegisterWebhook points Telegram at the given callback URL.
func (p *Provider) RegisterWebhook(ctx context.Context, webhookURL string) error {
	var lastErr error

	for attempt := 0; attempt <= len(registrationBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(registrationBackoff[attempt-1]):
			}
			util.Info("Retrying Telegram webhook registration", "attempt", attempt+1)
		}

		if err := p.setWebhook(ctx, webhookURL); err == nil {
			util.Info("Telegram webhook registered", "url", webhookURL)
			return nil
		} else {
			lastErr = err
			util.Warn("Telegram webhook registration failed", "attempt", attempt+1, "error", err)
		}
	}

	return fmt.Errorf("failed to register webhook after %d attempts: %w", len(registrationBackoff)+1, lastErr)
}

// setWebhook performs one registration attempt.
func (p *Provider) setWebhook(ctx context.Context, webhookURL string) error {
	secret := p.WebhookSecret()
	if secret == "" {
		// Registering without a secret would leave the callback endpoint
		// unauthenticated, so registration is refused instead.
		return fmt.Errorf("refusing to register a webhook without a secret token")
	}

	payload := map[string]any{
		"url":                  webhookURL,
		"allowed_updates":      []string{"message", "callback_query"},
		"drop_pending_updates": false,
		"secret_token":         secret,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	result, err := p.apiCall(ctx, "setWebhook", data)
	if err != nil {
		return err
	}

	var ok bool
	if err := json.Unmarshal(result, &ok); err != nil {
		return fmt.Errorf("failed to parse setWebhook response: %w", err)
	}
	if !ok {
		return fmt.Errorf("setWebhook returned false")
	}
	return nil
}

// DeleteWebhook stops Telegram from delivering updates.
func (p *Provider) DeleteWebhook(ctx context.Context) error {
	data, err := json.Marshal(map[string]any{"drop_pending_updates": false})
	if err != nil {
		return err
	}
	_, err = p.apiCall(ctx, "deleteWebhook", data)
	return err
}

// RegisterWebhookAsync registers the webhook in the background so startup is
// not blocked on an external service being reachable.
func (p *Provider) RegisterWebhookAsync(ctx context.Context, webhookURL string) {
	go func() {
		regCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()

		if err := p.RegisterWebhook(regCtx, webhookURL); err != nil {
			util.Error("Failed to register Telegram webhook", "error", err)
		}
	}()
}
