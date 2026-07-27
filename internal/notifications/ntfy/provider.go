// Package ntfy delivers notifications through an ntfy server.
package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

const defaultServer = "https://ntfy.sh"

// settings is an immutable configuration snapshot, swapped atomically when the
// operator changes notification settings.
type settings struct {
	enabled        bool
	server         string
	topic          string
	token          string
	priority       int
	minimalContent bool
}

// Provider implements ntfy notifications.
type Provider struct {
	current atomic.Pointer[settings]
	client  *http.Client
}

// NewProvider creates an ntfy provider. It is inert until Configure is called.
func NewProvider() *Provider {
	p := &Provider{client: notifications.NewHTTPClient(0)}
	p.current.Store(&settings{})
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return notifications.ProviderNtfy }

// Enabled reports whether ntfy is configured and switched on.
func (p *Provider) Enabled() bool {
	s := p.current.Load()
	return s.enabled && s.topic != ""
}

// Configure applies a settings snapshot.
func (p *Provider) Configure(creds *notifications.ProviderCredentials) {
	next := &settings{}
	if creds != nil {
		next.enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.NtfyCredentials); ok && c != nil {
			next.server = strings.TrimRight(c.ServerURL, "/")
			next.topic = c.Topic
			next.token = c.Token
			next.priority = mapPriority(c.Priority)
			next.minimalContent = c.MinimalContent
		}
	}
	if next.server == "" {
		next.server = defaultServer
	}
	if next.priority == 0 {
		next.priority = 4 // high, matching the documented default
	}
	p.current.Store(next)
}

// message is the ntfy publish payload.
type message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message"`
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
	Actions  []action `json:"actions,omitempty"`
}

type action struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url,omitempty"`
	Method string `json:"method,omitempty"`
	Clear  bool   `json:"clear,omitempty"`
}

// SendApproval sends an approval request with approve/deny actions.
func (p *Provider) SendApproval(ctx context.Context, n *notifications.ApprovalNotification) (string, error) {
	s := p.current.Load()

	var body strings.Builder
	if s.minimalContent {
		// Minimal mode keeps event details off the notification surface, for
		// topics that are not private.
		body.WriteString("A calendar request is awaiting approval.\n\nReview the details in SchedLock.\n")
	} else {
		body.WriteString(describe(n))
	}
	fmt.Fprintf(&body, "\nExpires in %s", n.ExpiresIn)

	reviewURL := n.ApprovePageURL
	if reviewURL == "" {
		reviewURL = n.WebURL
	}

	msg := message{
		Topic:    s.topic,
		Title:    n.Summary,
		Message:  body.String(),
		Priority: s.priority,
		Click:    reviewURL,
	}

	// The action buttons act immediately; the view button opens the page that
	// shows what is being approved first.
	if n.ApproveURL != "" && n.DenyURL != "" {
		msg.Actions = append(msg.Actions,
			action{Action: "http", Label: "Approve", URL: n.ApproveURL, Method: http.MethodPost, Clear: true},
			action{Action: "http", Label: "Deny", URL: n.DenyURL, Method: http.MethodPost, Clear: true},
		)
	}
	if reviewURL != "" {
		msg.Actions = append(msg.Actions, action{Action: "view", Label: "Review", URL: reviewURL})
	}

	return p.publish(ctx, s, &msg)
}

// SendResult reports the outcome of a decided request.
func (p *Provider) SendResult(ctx context.Context, n *notifications.ResultNotification) error {
	s := p.current.Load()

	priority := 3
	if n.Status == "failed" {
		priority = 4
	}

	_, err := p.publish(ctx, s, &message{
		Topic:    s.topic,
		Title:    resultTitle(n),
		Message:  n.Message,
		Priority: priority,
		Click:    n.EventURL,
	})
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	s := p.current.Load()
	_, err := p.publish(ctx, s, &message{
		Topic:    s.topic,
		Title:    "SchedLock test",
		Message:  "This is a test notification from SchedLock. If you can see this, ntfy is configured correctly.",
		Priority: 3,
	})
	return err
}

// publish posts a message to the ntfy server.
func (p *Provider) publish(ctx context.Context, s *settings, msg *message) (string, error) {
	if s.topic == "" {
		return "", fmt.Errorf("ntfy topic is not configured")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.server, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	body := notifications.ReadLimited(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ntfy returned status %d: %s", resp.StatusCode, util.TruncateString(string(body), 200))
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		// Delivery succeeded; only the message ID is unavailable.
		return "", nil
	}
	return response.ID, nil
}

// describe renders the operation details as plain text.
func describe(n *notifications.ApprovalNotification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Operation: %s\n", n.Operation)

	d := n.Details
	if d == nil {
		return b.String()
	}

	formatter := util.GetDefaultFormatter()
	if d.Title != "" {
		fmt.Fprintf(&b, "Event: %s\n", d.Title)
	}
	if !d.StartTime.IsZero() {
		fmt.Fprintf(&b, "Starts: %s\n", formatter.FormatDateTime(d.StartTime))
	}
	if !d.EndTime.IsZero() {
		fmt.Fprintf(&b, "Ends: %s\n", formatter.FormatDateTime(d.EndTime))
	}
	if d.Location != "" {
		fmt.Fprintf(&b, "Where: %s\n", d.Location)
	}
	if len(d.Attendees) > 0 {
		fmt.Fprintf(&b, "Attendees: %s\n", strings.Join(d.Attendees, ", "))
	}
	if d.EventID != "" {
		fmt.Fprintf(&b, "Event ID: %s\n", d.EventID)
	}
	return b.String()
}

func resultTitle(n *notifications.ResultNotification) string {
	switch n.Status {
	case "completed":
		return "Calendar request completed"
	case "failed":
		return "Calendar request failed"
	case "denied":
		return "Calendar request denied"
	case "expired":
		return "Calendar request expired"
	default:
		return fmt.Sprintf("Calendar request %s", n.Status)
	}
}

// mapPriority converts a configured priority name to the ntfy scale.
func mapPriority(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "max", "urgent":
		return 5
	case "high":
		return 4
	case "default":
		return 3
	case "low":
		return 2
	case "min":
		return 1
	default:
		return 0 // unset; the caller applies its default
	}
}
