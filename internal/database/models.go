// Package database provides shared model structs used across the application.
package database

import (
	"database/sql"
	"encoding/json"
	"time"
)

// APIKey represents an API key record.
type APIKey struct {
	ID                string
	KeyHash           string
	KeyPrefix         string
	Name              string
	Tier              string
	Constraints       *KeyConstraints
	CreatedAt         time.Time
	LastUsedAt        sql.NullTime
	ExpiresAt         sql.NullTime
	RevokedAt         sql.NullTime
	RateLimitOverride sql.NullInt64
	Metadata          json.RawMessage
}

// KeyConstraints defines per-key policy restrictions.
type KeyConstraints struct {
	CalendarAllowlist       []string          `json:"calendar_allowlist,omitempty"`
	Operations              map[string]string `json:"operations,omitempty"` // "create_event": "require_approval"
	MaxDurationMinutes      int               `json:"max_duration_minutes,omitempty"`
	AttendeeDomainAllowlist []string          `json:"attendee_domain_allowlist,omitempty"`
	AllowExternalAttendees  *bool             `json:"allow_external_attendees,omitempty"`
	MaxAttendees            int               `json:"max_attendees,omitempty"`
	BlockAllDayEvents       bool              `json:"block_all_day_events,omitempty"`
}

// Request represents a calendar operation request.
type Request struct {
	ID                string
	APIKeyID          string
	Operation         string
	Status            string
	Payload           json.RawMessage
	Result            json.RawMessage
	Error             sql.NullString
	SuggestionText    sql.NullString
	SuggestionAt      sql.NullTime
	SuggestionBy      sql.NullString
	CreatedAt         time.Time
	ExpiresAt         time.Time
	DecidedAt         sql.NullTime
	DecidedBy         sql.NullString
	ExecutedAt        sql.NullTime
	RetryCount        int
	WebhookNotifiedAt sql.NullTime
}

// RequestStatus constants
const (
	StatusPendingApproval  = "pending_approval"
	StatusChangeRequested  = "change_requested"
	StatusApproved         = "approved"
	StatusDenied           = "denied"
	StatusExpired          = "expired"
	StatusCancelled        = "cancelled"
	StatusExecuting        = "executing"
	StatusCompleted        = "completed"
	StatusFailed           = "failed"
)

// Operation constants
const (
	OperationCreateEvent = "create_event"
	OperationUpdateEvent = "update_event"
	OperationDeleteEvent = "delete_event"
)

// Tier constants
const (
	TierRead  = "read"
	TierWrite = "write"
	TierAdmin = "admin"
)

// AuditLogEntry represents an audit log record.
type AuditLogEntry struct {
	ID        int64
	Timestamp time.Time
	EventType string
	RequestID sql.NullString
	APIKeyID  sql.NullString
	Actor     sql.NullString
	Details   json.RawMessage
	IPAddress sql.NullString
}

// Audit event types
const (
	AuditAPIKeyCreated     = "api_key_created"
	AuditAPIKeyRevoked     = "api_key_revoked"
	AuditAPIKeyUsed        = "api_key_used"
	AuditRequestCreated    = "request_created"
	AuditRequestApproved   = "request_approved"
	AuditRequestDenied     = "request_denied"
	AuditRequestExpired    = "request_expired"
	AuditRequestChanged    = "request_change_requested"
	AuditRequestCancelled  = "request_cancelled"
	AuditRequestExecuting  = "request_executing"
	AuditRequestCompleted  = "request_completed"
	AuditRequestFailed     = "request_failed"
	AuditNotificationSent  = "notification_sent"
	AuditNotificationFailed = "notification_failed"
	AuditCallbackReceived  = "callback_received"
	AuditSettingsChanged   = "settings_changed"
	AuditOAuthConnected    = "oauth_connected"
	AuditOAuthRefreshed    = "oauth_refreshed"
	AuditOAuthFailed       = "oauth_failed"
	AuditLoginSuccess      = "login_success"
	AuditLoginFailed       = "login_failed"
	AuditSessionCreated    = "session_created"
	AuditSessionExpired    = "session_expired"
)

// NotificationLog represents a notification delivery record.
type NotificationLog struct {
	ID         int64
	RequestID  string
	Provider   string
	Status     string
	SentAt     time.Time
	CallbackAt sql.NullTime
	Error      sql.NullString
	Response   json.RawMessage
	MessageID  sql.NullString
}

// Notification providers
const (
	ProviderNtfy     = "ntfy"
	ProviderPushover = "pushover"
	ProviderTelegram = "telegram"
)

// Notification statuses
const (
	NotificationPending          = "pending"
	NotificationSent             = "sent"
	NotificationFailed           = "failed"
	NotificationCallbackReceived = "callback_received"
)

// Session represents a web UI session.
type Session struct {
	ID           string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastActivity sql.NullTime
	IPAddress    sql.NullString
	UserAgent    sql.NullString
	CSRFToken    string
}

// DecisionToken represents a single-use approval token.
type DecisionToken struct {
	TokenHash      string
	RequestID      string
	AllowedActions []string
	ExpiresAt      time.Time
	ConsumedAt     sql.NullTime
	ConsumedAction sql.NullString
	CreatedAt      time.Time
}

// WebhookFailure represents a failed Moltbot webhook delivery.
type WebhookFailure struct {
	ID         int64
	WebhookID  string
	RequestID  string
	Status     string
	Payload    json.RawMessage
	Error      sql.NullString
	Attempts   int
	CreatedAt  time.Time
	ResolvedAt sql.NullTime
}

// Setting represents a configuration setting.
type Setting struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
}

// OAuthToken represents stored OAuth credentials.
type OAuthToken struct {
	ID              string
	RefreshTokenEnc []byte
	Scopes          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
