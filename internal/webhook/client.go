// Package webhook provides Moltbot webhook delivery.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Client delivers webhooks to Moltbot.
type Client struct {
	config     *config.MoltbotConfig
	db         *database.DB
	httpClient *http.Client
}

// NewClient creates a new webhook client.
func NewClient(cfg *config.MoltbotConfig, db *database.DB) *Client {
	return &Client{
		config: cfg,
		db:     db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Enabled returns whether the webhook client is configured.
func (c *Client) Enabled() bool {
	return c.config.Webhook.URL != ""
}

// Deliver sends a webhook event to Moltbot.
func (c *Client) Deliver(ctx context.Context, event engine.WebhookEvent) error {
	if !c.Enabled() {
		return nil
	}

	payload := WebhookPayload{
		Event:     "request.status",
		RequestID: event.RequestID,
		Status:    event.Status,
		Message:   event.Message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if event.Suggestion != "" {
		payload.Suggestion = event.Suggestion
	}

	if len(event.Result) > 0 {
		payload.Result = event.Result
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	// Try to deliver with retries
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}

		err := c.doDelivery(ctx, data)
		if err == nil {
			util.Info("Webhook delivered successfully",
				"request_id", event.RequestID,
				"status", event.Status,
			)
			return nil
		}

		lastErr = err
		util.Warn("Webhook delivery failed",
			"attempt", attempt+1,
			"error", err,
		)
	}

	// Log the failure for retry
	c.logFailure(ctx, event.RequestID, data, lastErr)

	return lastErr
}

// doDelivery performs the actual HTTP request.
func (c *Client) doDelivery(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.Webhook.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SchedLock/1.0")

	// Add authentication header if configured
	if c.config.Webhook.Token != "" {
		signature := util.ComputeHMAC(data, c.config.Webhook.Token)
		req.Header.Set("X-SchedLock-Signature", signature)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// logFailure records a failed webhook delivery for later retry.
func (c *Client) logFailure(ctx context.Context, requestID string, payload []byte, err error) {
	_, dbErr := c.db.ExecContext(ctx, `
		INSERT INTO webhook_failures (request_id, payload, error_message)
		VALUES (?, ?, ?)
	`, requestID, string(payload), err.Error())

	if dbErr != nil {
		util.Error("Failed to log webhook failure", "error", dbErr)
	}
}

// RetryFailures attempts to redeliver failed webhooks.
func (c *Client) RetryFailures(ctx context.Context) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, request_id, payload, retry_count
		FROM webhook_failures
		WHERE retry_count < 5
		AND created_at > datetime('now', '-24 hours')
		ORDER BY created_at ASC
		LIMIT 10
	`)

	if err != nil {
		util.Error("Failed to query webhook failures", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id         int64
			requestID  string
			payload    string
			retryCount int
		)

		if err := rows.Scan(&id, &requestID, &payload, &retryCount); err != nil {
			continue
		}

		// Try to deliver
		err := c.doDelivery(ctx, []byte(payload))
		if err == nil {
			// Success - delete the failure record
			c.db.ExecContext(ctx, `DELETE FROM webhook_failures WHERE id = ?`, id)
			util.Info("Webhook retry succeeded", "request_id", requestID)
		} else {
			// Increment retry count
			c.db.ExecContext(ctx, `
				UPDATE webhook_failures
				SET retry_count = retry_count + 1, last_retry_at = datetime('now')
				WHERE id = ?
			`, id)
			util.Warn("Webhook retry failed",
				"request_id", requestID,
				"retry_count", retryCount+1,
				"error", err,
			)
		}
	}
}

// StartRetryWorker starts a background worker for retrying failed webhooks.
func (c *Client) StartRetryWorker(ctx context.Context) {
	if !c.Enabled() {
		return
	}

	util.Info("Starting webhook retry worker")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			util.Info("Webhook retry worker stopping")
			return
		case <-ticker.C:
			c.RetryFailures(ctx)
		}
	}
}
