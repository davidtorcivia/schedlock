// Package webhook delivers request status updates to the calling system.
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
	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/util"
)

// maxResponseBytes caps how much of a webhook response is read for logging, so
// a misbehaving endpoint cannot stream unbounded data into memory.
const maxResponseBytes = 8 << 10

// retryBatchSize limits how many failed deliveries are retried per sweep.
const retryBatchSize = 10

// Client delivers status webhooks and retries failures.
type Client struct {
	config     *config.MoltbotConfig
	db         *database.DB
	httpClient *http.Client
}

// NewClient creates a webhook client.
func NewClient(cfg *config.MoltbotConfig, db *database.DB) *Client {
	timeout := 30 * time.Second
	if cfg.Webhook.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Webhook.TimeoutSeconds) * time.Second
	}
	return &Client{
		config:     cfg,
		db:         db,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a destination is configured.
func (c *Client) Enabled() bool {
	return c.config.Webhook.URL != ""
}

// Deliver sends one status event, retrying transient failures before recording
// the payload for the background retry worker.
func (c *Client) Deliver(ctx context.Context, event engine.WebhookEvent) error {
	if !c.Enabled() {
		return nil
	}

	payload := Payload{
		Event:      EventRequestStatus,
		RequestID:  event.RequestID,
		Status:     event.Status,
		Message:    event.Message,
		Suggestion: event.Suggestion,
		Result:     event.Result,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	maxAttempts := max(c.config.Webhook.MaxRetries+1, 1)

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Backoff must observe cancellation: a sleeping delivery would
			// otherwise hold shutdown open for the length of the retry ladder.
			if err := sleep(ctx, c.backoffFor(attempt)); err != nil {
				lastErr = err
				break
			}
		}

		if err := c.post(ctx, data); err == nil {
			util.Info("Webhook delivered", "request_id", event.RequestID, "status", event.Status)
			return nil
		} else {
			lastErr = err
			util.Warn("Webhook delivery failed",
				"attempt", attempt+1, "request_id", event.RequestID, "error", err)
		}
	}

	c.recordFailure(ctx, event.RequestID, event.Status, data, lastErr)
	return lastErr
}

func (c *Client) backoffFor(attempt int) time.Duration {
	backoffs := c.config.Webhook.RetryBackoff
	idx := attempt - 1
	if idx < len(backoffs) {
		return time.Duration(backoffs[idx]) * time.Second
	}
	return time.Duration(attempt*2) * time.Second
}

// post performs one delivery attempt.
func (c *Client) post(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Webhook.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SchedLock/1.0")

	if c.config.Webhook.Token != "" {
		req.Header.Set("X-SchedLock-Signature", crypto.SignPayload(data, c.config.Webhook.Token))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, util.TruncateString(string(body), 200))
	}
	return nil
}

// recordFailure stores an undelivered payload for later retry.
func (c *Client) recordFailure(ctx context.Context, requestID, status string, payload []byte, cause error) {
	webhookID, err := crypto.GenerateWebhookID()
	if err != nil {
		webhookID = fmt.Sprintf("whk_%d", time.Now().UnixNano())
	}

	errText := ""
	if cause != nil {
		errText = cause.Error()
	}

	if _, err := c.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO webhook_failures (webhook_id, request_id, status, payload, error, attempts)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), 1)
	`, webhookID, requestID, status, string(payload), errText); err != nil {
		util.Error("Failed to record webhook failure", "error", err, "request_id", requestID)
	}
}

// pendingDelivery is one stored failure awaiting retry.
type pendingDelivery struct {
	id        int64
	webhookID string
	requestID string
	payload   string
	attempts  int
}

// RetryFailures re-attempts stored failures.
func (c *Client) RetryFailures(ctx context.Context) {
	pending, err := c.loadPending(ctx)
	if err != nil {
		util.Error("Failed to query webhook failures", "error", err)
		return
	}

	for _, delivery := range pending {
		if ctx.Err() != nil {
			return
		}

		if err := c.post(ctx, []byte(delivery.payload)); err != nil {
			if _, dbErr := c.db.ExecContext(ctx,
				`UPDATE webhook_failures SET attempts = attempts + 1, error = ? WHERE id = ?`,
				err.Error(), delivery.id); dbErr != nil {
				util.Error("Failed to record webhook retry", "error", dbErr)
			}
			util.Warn("Webhook retry failed",
				"request_id", delivery.requestID, "attempts", delivery.attempts+1, "error", err)
			continue
		}

		if _, dbErr := c.db.ExecContext(ctx,
			`UPDATE webhook_failures SET resolved_at = datetime('now') WHERE id = ?`,
			delivery.id); dbErr != nil {
			util.Error("Failed to mark webhook resolved", "error", dbErr)
		}
		util.Info("Webhook retry succeeded",
			"request_id", delivery.requestID, "webhook_id", delivery.webhookID)
	}
}

// loadPending reads the retry batch fully before any delivery runs.
//
// Holding a result set open while issuing writes on the same database keeps a
// connection checked out for the whole sweep, which under SQLite's single-writer
// model invites lock contention with the rest of the application.
func (c *Client) loadPending(ctx context.Context) ([]pendingDelivery, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, webhook_id, request_id, payload, attempts
		FROM webhook_failures
		WHERE resolved_at IS NULL AND attempts < ?
		ORDER BY created_at ASC
		LIMIT ?
	`, c.config.Webhook.MaxRetries+1, retryBatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pending []pendingDelivery
	for rows.Next() {
		var d pendingDelivery
		if err := rows.Scan(&d.id, &d.webhookID, &d.requestID, &d.payload, &d.attempts); err != nil {
			return nil, err
		}
		pending = append(pending, d)
	}

	return pending, rows.Err()
}

// StartRetryWorker periodically retries stored failures until ctx is done.
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

// sleep waits for d, returning early if the context is cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
