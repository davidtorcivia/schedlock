// Package webhook delivers notifications to a caller-supplied HTTP endpoint.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

// settings is an immutable configuration snapshot.
type settings struct {
	enabled bool
	url     string
	secret  string
	client  *http.Client
}

// Provider implements generic webhook notifications, for integrations without
// a dedicated provider (home automation, chat bridges, monitoring).
type Provider struct {
	current atomic.Pointer[settings]
}

// NewProvider creates a webhook provider. It is inert until Configure runs.
func NewProvider() *Provider {
	p := &Provider{}
	p.current.Store(&settings{client: notifications.NewHTTPClient(0)})
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return notifications.ProviderWebhook }

// Enabled reports whether a destination is configured and switched on.
func (p *Provider) Enabled() bool {
	s := p.current.Load()
	return s.enabled && s.url != ""
}

// Configure applies a settings snapshot, rebuilding the HTTP client so a
// changed timeout takes effect without a restart.
func (p *Provider) Configure(creds *notifications.ProviderCredentials) {
	next := &settings{}
	timeout := time.Duration(0)

	if creds != nil {
		next.enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.WebhookCredentials); ok && c != nil {
			next.url = c.URL
			next.secret = c.Secret
			timeout = time.Duration(c.TimeoutSeconds) * time.Second
		}
	}

	next.client = notifications.NewHTTPClient(timeout)
	p.current.Store(next)
}

// Payload is the JSON body delivered to the configured endpoint.
type Payload struct {
	Event     string   `json:"event"`
	Timestamp string   `json:"timestamp"`
	RequestID string   `json:"request_id"`
	Operation string   `json:"operation"`
	Summary   string   `json:"summary,omitempty"`
	Status    string   `json:"status,omitempty"`
	Message   string   `json:"message,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	URLs      *URLs    `json:"urls,omitempty"`
	Details   *Details `json:"details,omitempty"`
}

// URLs are the action links offered with an approval request.
type URLs struct {
	Approve     string `json:"approve,omitempty"`
	Deny        string `json:"deny,omitempty"`
	Suggest     string `json:"suggest,omitempty"`
	Web         string `json:"web,omitempty"`
	ApprovePage string `json:"approve_page,omitempty"`
}

// Details describes the requested calendar operation.
type Details struct {
	Title       string   `json:"title,omitempty"`
	StartTime   string   `json:"start_time,omitempty"`
	EndTime     string   `json:"end_time,omitempty"`
	Location    string   `json:"location,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	Description string   `json:"description,omitempty"`
	CalendarID  string   `json:"calendar_id,omitempty"`
	EventID     string   `json:"event_id,omitempty"`
	IsPartial   bool     `json:"is_partial,omitempty"`
}

// SendApproval posts an approval request.
func (p *Provider) SendApproval(ctx context.Context, n *notifications.ApprovalNotification) (string, error) {
	payload := Payload{
		Event:     "approval_request",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: n.RequestID,
		Operation: n.Operation,
		Summary:   n.Summary,
		ExpiresAt: n.ExpiresAt.UTC().Format(time.RFC3339),
		URLs: &URLs{
			Approve:     n.ApproveURL,
			Deny:        n.DenyURL,
			Suggest:     n.SuggestURL,
			Web:         n.WebURL,
			ApprovePage: n.ApprovePageURL,
		},
	}

	if d := n.Details; d != nil {
		payload.Details = &Details{
			Title:       d.Title,
			Location:    d.Location,
			Attendees:   d.Attendees,
			Description: d.Description,
			CalendarID:  d.CalendarID,
			EventID:     d.EventID,
			IsPartial:   d.IsPartial,
		}
		if !d.StartTime.IsZero() {
			payload.Details.StartTime = d.StartTime.Format(time.RFC3339)
		}
		if !d.EndTime.IsZero() {
			payload.Details.EndTime = d.EndTime.Format(time.RFC3339)
		}
	}

	if err := p.post(ctx, payload); err != nil {
		return "", err
	}
	return n.RequestID, nil
}

// SendResult posts the outcome of a decided request.
func (p *Provider) SendResult(ctx context.Context, n *notifications.ResultNotification) error {
	return p.post(ctx, Payload{
		Event:     "request_result",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: n.RequestID,
		Operation: n.Operation,
		Status:    n.Status,
		Message:   n.Message,
	})
}

// SendTest posts a test payload.
func (p *Provider) SendTest(ctx context.Context) error {
	return p.post(ctx, Payload{
		Event:     "test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: "test",
		Operation: "test",
		Summary:   "Test notification from SchedLock",
		Message:   "If you receive this, webhook notifications are configured correctly.",
	})
}

// post delivers a payload, signing it when a secret is configured.
func (p *Provider) post(ctx context.Context, payload Payload) error {
	s := p.current.Load()
	if s.url == "" {
		return fmt.Errorf("webhook URL is not configured")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SchedLock/1.0")

	if s.secret != "" {
		signature := crypto.SignPayload(data, s.secret)
		req.Header.Set("X-SchedLock-Signature", signature)
		req.Header.Set("X-Signature-256", "sha256="+signature)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	body := notifications.ReadLimited(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, util.TruncateString(string(body), 200))
	}
	return nil
}
