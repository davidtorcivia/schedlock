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
}

// KeyConstraints defines per-key policy restrictions.
type KeyConstraints struct {
	CalendarAllowlist       []string          `json:"calendar_allowlist,omitempty"`
	Operations              map[string]string `json:"operations,omitempty"` // e.g. "create_event": "require_approval"
	MaxDurationMinutes      int               `json:"max_duration_minutes,omitempty"`
	AttendeeDomainAllowlist []string          `json:"attendee_domain_allowlist,omitempty"`
	AllowExternalAttendees  *bool             `json:"allow_external_attendees,omitempty"`
	MaxAttendees            int               `json:"max_attendees,omitempty"`
	BlockAllDayEvents       bool              `json:"block_all_day_events,omitempty"`
}

// Operation policy values usable in KeyConstraints.Operations.
const (
	// OperationPolicyDeny rejects the operation outright.
	OperationPolicyDeny = "deny"
	// OperationPolicyRequireApproval forces human approval regardless of tier.
	OperationPolicyRequireApproval = "require_approval"
	// OperationPolicyAuto executes without approval, subject to the remaining
	// constraints.
	OperationPolicyAuto = "auto"
)

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

// Request status values.
const (
	StatusPendingApproval = "pending_approval"
	StatusChangeRequested = "change_requested"
	StatusApproved        = "approved"
	StatusDenied          = "denied"
	StatusExpired         = "expired"
	StatusCancelled       = "cancelled"
	StatusExecuting       = "executing"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
)

// Operation values.
const (
	OperationCreateEvent = "create_event"
	OperationUpdateEvent = "update_event"
	OperationDeleteEvent = "delete_event"
)

// API key tiers.
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

// Audit event types.
const (
	AuditAPIKeyCreated      = "api_key_created"
	AuditAPIKeyRevoked      = "api_key_revoked"
	AuditRequestCreated     = "request_created"
	AuditRequestApproved    = "request_approved"
	AuditRequestDenied      = "request_denied"
	AuditRequestExpired     = "request_expired"
	AuditRequestChanged     = "request_change_requested"
	AuditRequestCancelled   = "request_cancelled"
	AuditRequestEdited      = "request_edited"
	AuditRequestExecuting   = "request_executing"
	AuditRequestCompleted   = "request_completed"
	AuditRequestFailed      = "request_failed"
	AuditNotificationSent   = "notification_sent"
	AuditNotificationFailed = "notification_failed"
	AuditSettingsChanged    = "settings_changed"
	AuditOAuthConnected     = "oauth_connected"
	AuditOAuthFailed        = "oauth_failed"
	AuditLoginSuccess       = "login_success"
	AuditLoginFailed        = "login_failed"
	AuditLogout             = "logout"
)

// Notification providers.
const (
	ProviderNtfy     = "ntfy"
	ProviderPushover = "pushover"
	ProviderTelegram = "telegram"
	ProviderWebhook  = "webhook"
)

// Notification delivery statuses.
const (
	NotificationSent             = "sent"
	NotificationFailed           = "failed"
	NotificationCallbackReceived = "callback_received"
)
