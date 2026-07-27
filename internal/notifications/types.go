// Package notifications delivers approval requests and outcomes to the humans
// who approve them.
package notifications

import (
	"context"
	"encoding/json"
	"time"
)

// Provider names.
const (
	ProviderNtfy     = "ntfy"
	ProviderPushover = "pushover"
	ProviderTelegram = "telegram"
	ProviderWebhook  = "webhook"
	// ProviderGoogleOAuth is stored alongside the notification providers in the
	// encrypted credential table, but is not a notification provider itself.
	ProviderGoogleOAuth = "google_oauth"
)

// Provider delivers notifications through one channel.
type Provider interface {
	// Name returns the provider's stable identifier.
	Name() string

	// Enabled reports whether the provider is configured and switched on.
	Enabled() bool

	// Configure applies a settings snapshot. Providers are reconfigured while
	// the server runs, so implementations must be safe to call concurrently
	// with delivery.
	Configure(creds *ProviderCredentials)

	// SendApproval requests a decision, returning a provider message ID when
	// one is available (Telegram needs it to match replies to requests).
	SendApproval(ctx context.Context, notification *ApprovalNotification) (messageID string, err error)

	// SendResult reports the outcome of a decided request.
	SendResult(ctx context.Context, notification *ResultNotification) error

	// SendTest sends a test message to verify configuration.
	SendTest(ctx context.Context) error
}

// CallbackHandler processes decisions arriving from a provider.
type CallbackHandler interface {
	HandleCallback(ctx context.Context, callback *Callback) error
}

// ApprovalNotification is a request for a human decision.
type ApprovalNotification struct {
	RequestID string
	Operation string
	Summary   string
	Details   *EventDetails

	// ApproveURL and DenyURL act immediately when POSTed; they are for
	// programmatic actions such as ntfy action buttons.
	ApproveURL string
	DenyURL    string
	SuggestURL string

	// ApprovePageURL opens the confirmation page a human can read before
	// deciding. Links a person might click go here.
	ApprovePageURL string

	// WebURL is the authenticated request detail page.
	WebURL string

	ExpiresAt     time.Time
	ExpiresIn     string
	DecisionToken string
}

// EventDetails is the human-readable description of a requested operation.
type EventDetails struct {
	Title       string
	StartTime   time.Time
	EndTime     time.Time
	Location    string
	Attendees   []string
	Description string
	CalendarID  string
	EventID     string
	// IsPartial marks an update, where absent fields are left unchanged rather
	// than cleared.
	IsPartial bool
}

// ResultNotification reports what happened to a decided request.
type ResultNotification struct {
	RequestID string
	Operation string
	Status    string
	Message   string
	EventURL  string
	Error     string
	Result    json.RawMessage
}

// Callback is a decision arriving from a provider.
type Callback struct {
	Provider    string
	RequestID   string
	Action      string // "approve", "deny", or "suggest"
	Suggestion  string
	MessageID   string
	ChatID      string
	RespondedBy string
}

// DeliveryRecord is a logged notification delivery.
type DeliveryRecord struct {
	ID           int64
	RequestID    string
	Provider     string
	Status       string
	MessageID    string
	SentAt       time.Time
	CallbackAt   *time.Time
	ErrorMessage string
}
