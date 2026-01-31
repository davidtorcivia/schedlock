// Package webhook provides generic webhook notification delivery.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/notifications"
)

// Provider implements generic webhook notifications.
type Provider struct {
	config *config.GenericWebhookConfig
	client *http.Client
}

// NewProvider creates a new webhook provider.
func NewProvider(cfg *config.GenericWebhookConfig) *Provider {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Provider{
		config: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "webhook"
}

// Enabled returns whether the webhook is configured and enabled.
func (p *Provider) Enabled() bool {
	return p.config.Enabled && p.config.URL != ""
}

// WebhookPayload is the JSON structure sent to the webhook.
type WebhookPayload struct {
	Event     string                 `json:"event"`
	Timestamp string                 `json:"timestamp"`
	RequestID string                 `json:"request_id"`
	Operation string                 `json:"operation"`
	Summary   string                 `json:"summary,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Message   string                 `json:"message,omitempty"`
	ExpiresAt string                 `json:"expires_at,omitempty"`
	URLs      *WebhookURLs           `json:"urls,omitempty"`
	Details   *WebhookEventDetails   `json:"details,omitempty"`
}

// WebhookURLs contains the approval/deny URLs.
type WebhookURLs struct {
	Approve     string `json:"approve,omitempty"`
	Deny        string `json:"deny,omitempty"`
	Web         string `json:"web,omitempty"`
	ApprovePage string `json:"approve_page,omitempty"`
}

// WebhookEventDetails contains event details.
type WebhookEventDetails struct {
	Title       string   `json:"title,omitempty"`
	StartTime   string   `json:"start_time,omitempty"`
	EndTime     string   `json:"end_time,omitempty"`
	Location    string   `json:"location,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	Description string   `json:"description,omitempty"`
	CalendarID  string   `json:"calendar_id,omitempty"`
	EventID     string   `json:"event_id,omitempty"`
}

// SendApproval sends an approval request notification.
func (p *Provider) SendApproval(ctx context.Context, notification *notifications.ApprovalNotification) (string, error) {
	payload := WebhookPayload{
		Event:     "approval_request",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: notification.RequestID,
		Operation: notification.Operation,
		Summary:   notification.Summary,
		ExpiresAt: notification.ExpiresAt.Format(time.RFC3339),
		URLs: &WebhookURLs{
			Approve:     notification.ApproveURL,
			Deny:        notification.DenyURL,
			Web:         notification.WebURL,
			ApprovePage: notification.ApprovePageURL,
		},
	}

	if notification.Details != nil {
		payload.Details = &WebhookEventDetails{
			Title:       notification.Details.Title,
			Location:    notification.Details.Location,
			Attendees:   notification.Details.Attendees,
			Description: notification.Details.Description,
			CalendarID:  notification.Details.CalendarID,
			EventID:     notification.Details.EventID,
		}
		if !notification.Details.StartTime.IsZero() {
			payload.Details.StartTime = notification.Details.StartTime.Format(time.RFC3339)
		}
		if !notification.Details.EndTime.IsZero() {
			payload.Details.EndTime = notification.Details.EndTime.Format(time.RFC3339)
		}
	}

	return p.send(ctx, payload)
}

// SendResult sends a result notification.
func (p *Provider) SendResult(ctx context.Context, notification *notifications.ResultNotification) error {
	payload := WebhookPayload{
		Event:     "request_result",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: notification.RequestID,
		Operation: notification.Operation,
		Status:    notification.Status,
		Message:   notification.Message,
	}

	_, err := p.send(ctx, payload)
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	payload := WebhookPayload{
		Event:     "test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: "test_" + time.Now().Format("20060102150405"),
		Operation: "test",
		Summary:   "Test notification from SchedLock",
		Message:   "If you receive this, webhook notifications are configured correctly.",
	}

	_, err := p.send(ctx, payload)
	return err
}

// send sends the payload to the webhook URL.
func (p *Provider) send(ctx context.Context, payload WebhookPayload) (string, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.config.URL, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SchedLock/1.0")

	// Add HMAC signature if secret is configured
	if p.config.Secret != "" {
		signature := p.computeSignature(jsonData)
		req.Header.Set("X-SchedLock-Signature", signature)
		req.Header.Set("X-Signature-256", "sha256="+signature)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error messages
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(body))
	}

	// Use request ID as message ID
	return payload.RequestID, nil
}

// computeSignature computes HMAC-SHA256 signature of the payload.
func (p *Provider) computeSignature(payload []byte) string {
	h := hmac.New(sha256.New, []byte(p.config.Secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
