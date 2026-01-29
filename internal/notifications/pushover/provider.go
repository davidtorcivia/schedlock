// Package pushover provides Pushover notification delivery.
package pushover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/notifications"
)

const pushoverAPIURL = "https://api.pushover.net/1/messages.json"

// Provider implements Pushover notifications.
type Provider struct {
	config *config.PushoverConfig
	client *http.Client
}

// NewProvider creates a new Pushover provider.
func NewProvider(cfg *config.PushoverConfig) *Provider {
	return &Provider{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "pushover"
}

// Enabled returns whether Pushover is configured and enabled.
func (p *Provider) Enabled() bool {
	return p.config.Enabled && p.config.AppToken != "" && p.config.UserKey != ""
}

// SendApproval sends an approval request notification.
func (p *Provider) SendApproval(ctx context.Context, notification *notifications.ApprovalNotification) (string, error) {
	title := fmt.Sprintf("Calendar: %s", notification.Summary)

	var body strings.Builder
	body.WriteString(fmt.Sprintf("<b>Operation:</b> %s\n", notification.Operation))

	if notification.Details != nil {
		if notification.Details.Title != "" {
			body.WriteString(fmt.Sprintf("<b>Event:</b> %s\n", notification.Details.Title))
		}
		if !notification.Details.StartTime.IsZero() {
			body.WriteString(fmt.Sprintf("<b>When:</b> %s\n", notification.Details.StartTime.Format("Mon Jan 2, 3:04 PM")))
		}
		if notification.Details.Location != "" {
			body.WriteString(fmt.Sprintf("<b>Where:</b> %s\n", notification.Details.Location))
		}
		if len(notification.Details.Attendees) > 0 {
			body.WriteString(fmt.Sprintf("<b>Attendees:</b> %s\n", strings.Join(notification.Details.Attendees, ", ")))
		}
	}

	body.WriteString(fmt.Sprintf("\n<b>Expires:</b> %s\n\n", notification.ExpiresIn))
	if notification.ApproveURL != "" && notification.DenyURL != "" {
		body.WriteString(fmt.Sprintf("<a href=\"%s\">Approve</a> | ", notification.ApproveURL))
		body.WriteString(fmt.Sprintf("<a href=\"%s\">Deny</a> | ", notification.DenyURL))
	}
	if notification.WebURL != "" {
		body.WriteString(fmt.Sprintf("<a href=\"%s\">Review</a>", notification.WebURL))
	}

	params := url.Values{
		"token":     {p.config.AppToken},
		"user":      {p.config.UserKey},
		"title":     {title},
		"message":   {body.String()},
		"html":      {"1"},
		"priority":  {"1"}, // High priority
		"url":       {notification.WebURL},
		"url_title": {"Open in SchedLock"},
	}

	return p.send(ctx, params)
}

// SendResult sends a result notification.
func (p *Provider) SendResult(ctx context.Context, notification *notifications.ResultNotification) error {
	var title string
	priority := "0" // Normal priority

	switch notification.Status {
	case "completed":
		title = fmt.Sprintf("%s Completed", notification.Operation)
	case "failed":
		title = fmt.Sprintf("%s Failed", notification.Operation)
		priority = "1" // High priority for failures
	case "denied":
		title = fmt.Sprintf("%s Denied", notification.Operation)
	case "expired":
		title = fmt.Sprintf("%s Expired", notification.Operation)
	default:
		title = fmt.Sprintf("%s: %s", notification.Operation, notification.Status)
	}

	params := url.Values{
		"token":    {p.config.AppToken},
		"user":     {p.config.UserKey},
		"title":    {title},
		"message":  {notification.Message},
		"priority": {priority},
	}

	if notification.EventURL != "" {
		params.Set("url", notification.EventURL)
		params.Set("url_title", "View Event")
	}

	_, err := p.send(ctx, params)
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	params := url.Values{
		"token":    {p.config.AppToken},
		"user":     {p.config.UserKey},
		"title":    {"SchedLock Test"},
		"message":  {"This is a test notification from SchedLock. If you can see this, Pushover is configured correctly."},
		"priority": {"0"},
	}

	_, err := p.send(ctx, params)
	return err
}

// send sends a message to Pushover and returns the receipt/request ID.
func (p *Provider) send(ctx context.Context, params url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", pushoverAPIURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var response struct {
		Status  int    `json:"status"`
		Request string `json:"request"`
		Errors  []string `json:"errors,omitempty"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if response.Status != 1 {
		errMsg := "unknown error"
		if len(response.Errors) > 0 {
			errMsg = strings.Join(response.Errors, ", ")
		}
		return "", fmt.Errorf("pushover error: %s", errMsg)
	}

	return response.Request, nil
}
