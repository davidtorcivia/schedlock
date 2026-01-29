// Package ntfy provides ntfy.sh notification delivery.
package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/notifications"
)

// Provider implements ntfy notifications.
type Provider struct {
	config *config.NtfyConfig
	client *http.Client
}

// NewProvider creates a new ntfy provider.
func NewProvider(cfg *config.NtfyConfig) *Provider {
	return &Provider{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "ntfy"
}

// Enabled returns whether ntfy is configured and enabled.
func (p *Provider) Enabled() bool {
	return p.config.Enabled && p.config.Topic != ""
}

// ntfyMessage represents the ntfy API message format.
type ntfyMessage struct {
	Topic    string       `json:"topic"`
	Title    string       `json:"title,omitempty"`
	Message  string       `json:"message"`
	Priority int          `json:"priority,omitempty"`
	Tags     []string     `json:"tags,omitempty"`
	Click    string       `json:"click,omitempty"`
	Actions  []ntfyAction `json:"actions,omitempty"`
}

type ntfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url,omitempty"`
	Method string `json:"method,omitempty"`
	Clear  bool   `json:"clear,omitempty"`
}

// SendApproval sends an approval request notification.
func (p *Provider) SendApproval(ctx context.Context, notification *notifications.ApprovalNotification) (string, error) {
	title := fmt.Sprintf("📅 %s", notification.Summary)

	var body strings.Builder
	body.WriteString(fmt.Sprintf("Operation: %s\n", notification.Operation))

	if notification.Details != nil {
		if notification.Details.Title != "" {
			body.WriteString(fmt.Sprintf("Event: %s\n", notification.Details.Title))
		}
		if !notification.Details.StartTime.IsZero() {
			body.WriteString(fmt.Sprintf("When: %s\n", notification.Details.StartTime.Format("Mon Jan 2, 3:04 PM")))
		}
		if notification.Details.Location != "" {
			body.WriteString(fmt.Sprintf("Where: %s\n", notification.Details.Location))
		}
		if len(notification.Details.Attendees) > 0 {
			body.WriteString(fmt.Sprintf("Attendees: %s\n", strings.Join(notification.Details.Attendees, ", ")))
		}
	}

	body.WriteString(fmt.Sprintf("\nExpires: %s", notification.ExpiresIn))

	msg := ntfyMessage{
		Topic:    p.config.Topic,
		Title:    title,
		Message:  body.String(),
		Priority: 4, // High priority
		Tags:     []string{"calendar", "approval"},
		Click:    notification.WebURL,
		Actions: []ntfyAction{
			{
				Action: "http",
				Label:  "✅ Approve",
				URL:    notification.ApproveURL,
				Method: "POST",
				Clear:  true,
			},
			{
				Action: "http",
				Label:  "❌ Deny",
				URL:    notification.DenyURL,
				Method: "POST",
				Clear:  true,
			},
			{
				Action: "view",
				Label:  "📝 Review",
				URL:    notification.WebURL,
			},
		},
	}

	return p.send(ctx, &msg)
}

// SendResult sends a result notification.
func (p *Provider) SendResult(ctx context.Context, notification *notifications.ResultNotification) error {
	var title string
	var tags []string
	priority := 3 // Default priority

	switch notification.Status {
	case "completed":
		title = fmt.Sprintf("✅ %s Completed", notification.Operation)
		tags = []string{"white_check_mark", "calendar"}
	case "failed":
		title = fmt.Sprintf("❌ %s Failed", notification.Operation)
		tags = []string{"x", "calendar"}
		priority = 4
	case "denied":
		title = fmt.Sprintf("🚫 %s Denied", notification.Operation)
		tags = []string{"no_entry", "calendar"}
	case "expired":
		title = fmt.Sprintf("⏰ %s Expired", notification.Operation)
		tags = []string{"alarm_clock", "calendar"}
	default:
		title = fmt.Sprintf("📅 %s: %s", notification.Operation, notification.Status)
		tags = []string{"calendar"}
	}

	msg := ntfyMessage{
		Topic:    p.config.Topic,
		Title:    title,
		Message:  notification.Message,
		Priority: priority,
		Tags:     tags,
	}

	if notification.EventURL != "" {
		msg.Click = notification.EventURL
	}

	_, err := p.send(ctx, &msg)
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	msg := ntfyMessage{
		Topic:    p.config.Topic,
		Title:    "🧪 SchedLock Test",
		Message:  "This is a test notification from SchedLock. If you can see this, ntfy is configured correctly!",
		Priority: 3,
		Tags:     []string{"test_tube", "calendar"},
	}

	_, err := p.send(ctx, &msg)
	return err
}

// send sends a message to ntfy and returns the message ID.
func (p *Provider) send(ctx context.Context, msg *ntfyMessage) (string, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	serverURL := p.config.Server
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", serverURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add authentication if configured
	if p.config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.Token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ntfy returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to get message ID
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		// If we can't parse the response, return empty ID but no error
		return "", nil
	}

	return response.ID, nil
}
