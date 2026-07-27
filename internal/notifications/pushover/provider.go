// Package pushover delivers notifications through Pushover.
package pushover

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

const apiURL = "https://api.pushover.net/1/messages.json"

// settings is an immutable configuration snapshot.
type settings struct {
	enabled  bool
	appToken string
	userKey  string
	priority int
	sound    string
}

// Provider implements Pushover notifications.
type Provider struct {
	current atomic.Pointer[settings]
	client  *http.Client
}

// NewProvider creates a Pushover provider. It is inert until Configure runs.
func NewProvider() *Provider {
	p := &Provider{client: notifications.NewHTTPClient(0)}
	p.current.Store(&settings{})
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return notifications.ProviderPushover }

// Enabled reports whether Pushover is configured and switched on.
func (p *Provider) Enabled() bool {
	s := p.current.Load()
	return s.enabled && s.appToken != "" && s.userKey != ""
}

// Configure applies a settings snapshot.
func (p *Provider) Configure(creds *notifications.ProviderCredentials) {
	next := &settings{priority: 1}
	if creds != nil {
		next.enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.PushoverCredentials); ok && c != nil {
			next.appToken = c.AppToken
			next.userKey = c.UserKey
			next.priority = clampPriority(c.Priority)
			next.sound = c.Sound
		}
	}
	p.current.Store(next)
}

// SendApproval sends an approval request.
func (p *Provider) SendApproval(ctx context.Context, n *notifications.ApprovalNotification) (string, error) {
	s := p.current.Load()

	reviewURL := n.ApprovePageURL
	if reviewURL == "" {
		reviewURL = n.WebURL
	}

	params := url.Values{
		"token":    {s.appToken},
		"user":     {s.userKey},
		"title":    {util.TruncateString(n.Summary, 250)},
		"message":  {describe(n)},
		"html":     {"1"},
		"priority": {strconv.Itoa(s.priority)},
	}
	if reviewURL != "" {
		params.Set("url", reviewURL)
		params.Set("url_title", "Review and decide")
	}
	if s.sound != "" {
		params.Set("sound", s.sound)
	}
	if s.priority >= 2 {
		// Emergency priority requires a retry interval and an expiry.
		params.Set("retry", "60")
		params.Set("expire", "3600")
	}

	return p.send(ctx, params)
}

// SendResult reports the outcome of a decided request.
func (p *Provider) SendResult(ctx context.Context, n *notifications.ResultNotification) error {
	s := p.current.Load()

	priority := 0
	if n.Status == "failed" {
		priority = 1
	}

	params := url.Values{
		"token":    {s.appToken},
		"user":     {s.userKey},
		"title":    {resultTitle(n)},
		"message":  {n.Message},
		"priority": {strconv.Itoa(priority)},
	}
	if n.EventURL != "" {
		params.Set("url", n.EventURL)
		params.Set("url_title", "View event")
	}
	if s.sound != "" {
		params.Set("sound", s.sound)
	}

	_, err := p.send(ctx, params)
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	s := p.current.Load()
	_, err := p.send(ctx, url.Values{
		"token":    {s.appToken},
		"user":     {s.userKey},
		"title":    {"SchedLock test"},
		"message":  {"This is a test notification from SchedLock. If you can see this, Pushover is configured correctly."},
		"priority": {"0"},
	})
	return err
}

// send posts a message to the Pushover API.
func (p *Provider) send(ctx context.Context, params url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	body := notifications.ReadLimited(resp.Body)

	var response struct {
		Status  int      `json:"status"`
		Request string   `json:"request"`
		Errors  []string `json:"errors,omitempty"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("pushover returned status %d with an unreadable body", resp.StatusCode)
	}

	if response.Status != 1 {
		reason := "unknown error"
		if len(response.Errors) > 0 {
			reason = strings.Join(response.Errors, ", ")
		}
		return "", fmt.Errorf("pushover error: %s", reason)
	}

	return response.Request, nil
}

// describe renders the operation details as Pushover's limited HTML.
//
// Every interpolated value is escaped: an event title containing "<" or "&"
// would otherwise be rejected by Pushover as malformed markup, dropping the
// notification entirely.
func describe(n *notifications.ApprovalNotification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>Operation:</b> %s\n", html.EscapeString(n.Operation))

	if d := n.Details; d != nil {
		formatter := util.GetDefaultFormatter()
		if d.Title != "" {
			fmt.Fprintf(&b, "<b>Event:</b> %s\n", html.EscapeString(d.Title))
		}
		if !d.StartTime.IsZero() {
			fmt.Fprintf(&b, "<b>Starts:</b> %s\n", html.EscapeString(formatter.FormatDateTime(d.StartTime)))
		}
		if !d.EndTime.IsZero() {
			fmt.Fprintf(&b, "<b>Ends:</b> %s\n", html.EscapeString(formatter.FormatDateTime(d.EndTime)))
		}
		if d.Location != "" {
			fmt.Fprintf(&b, "<b>Where:</b> %s\n", html.EscapeString(d.Location))
		}
		if len(d.Attendees) > 0 {
			fmt.Fprintf(&b, "<b>Attendees:</b> %s\n", html.EscapeString(strings.Join(d.Attendees, ", ")))
		}
	}

	fmt.Fprintf(&b, "\n<b>Expires in:</b> %s", html.EscapeString(n.ExpiresIn))
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

// clampPriority keeps the configured priority inside Pushover's range.
func clampPriority(priority int) int {
	if priority < -2 {
		return -2
	}
	if priority > 2 {
		return 2
	}
	return priority
}
