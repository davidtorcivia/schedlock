package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// WebhookInfo contains information about the current webhook.
type WebhookInfo struct {
	URL                  string `json:"url"`
	HasCustomCertificate bool   `json:"has_custom_certificate"`
	PendingUpdateCount   int    `json:"pending_update_count"`
	LastErrorDate        int64  `json:"last_error_date,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	MaxConnections       int    `json:"max_connections,omitempty"`
}

// RegisterWebhook registers the webhook URL with Telegram.
// Includes retry logic for Cloudflare Tunnel startup delays.
func (p *Provider) RegisterWebhook(ctx context.Context, webhookURL string) error {
	// Retry configuration for tunnel delays
	maxRetries := 5
	backoffSeconds := []int{2, 5, 10, 20, 30}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(backoffSeconds[attempt-1]) * time.Second
			util.Info("Retrying webhook registration",
				"attempt", attempt+1,
				"backoff", backoff,
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		err := p.setWebhook(ctx, webhookURL)
		if err == nil {
			util.Info("Telegram webhook registered successfully", "url", webhookURL)
			return nil
		}

		lastErr = err
		util.Warn("Webhook registration failed",
			"attempt", attempt+1,
			"error", err,
		)
	}

	return fmt.Errorf("failed to register webhook after %d attempts: %w", maxRetries, lastErr)
}

// setWebhook sets the webhook URL.
func (p *Provider) setWebhook(ctx context.Context, webhookURL string) error {
	req := map[string]interface{}{
		"url":             webhookURL,
		"allowed_updates": []string{"message", "callback_query"},
		"drop_pending_updates": false,
	}
	if p.config.WebhookSecret != "" {
		req["secret_token"] = p.config.WebhookSecret
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	result, err := p.apiCall(ctx, "setWebhook", data)
	if err != nil {
		return err
	}

	var success bool
	if err := json.Unmarshal(result, &success); err != nil {
		return fmt.Errorf("failed to parse setWebhook response: %w", err)
	}

	if !success {
		return fmt.Errorf("setWebhook returned false")
	}

	return nil
}

// GetWebhookInfo returns the current webhook configuration.
func (p *Provider) GetWebhookInfo(ctx context.Context) (*WebhookInfo, error) {
	result, err := p.apiCall(ctx, "getWebhookInfo", nil)
	if err != nil {
		return nil, err
	}

	var info WebhookInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, fmt.Errorf("failed to parse webhook info: %w", err)
	}

	return &info, nil
}

// DeleteWebhook removes the webhook.
func (p *Provider) DeleteWebhook(ctx context.Context) error {
	req := map[string]interface{}{
		"drop_pending_updates": false,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	_, err = p.apiCall(ctx, "deleteWebhook", data)
	return err
}

// RegisterWebhookAsync registers the webhook in the background.
// This is useful during startup when the tunnel might not be ready yet.
func (p *Provider) RegisterWebhookAsync(webhookURL string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := p.RegisterWebhook(ctx, webhookURL); err != nil {
			util.Error("Failed to register Telegram webhook", "error", err)
		}
	}()
}
