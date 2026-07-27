package config

import "time"

// Server defaults.
const (
	DefaultHost         = "0.0.0.0"
	DefaultPort         = 8080
	DefaultBaseURL      = "http://localhost:8080"
	DefaultReadTimeout  = 30 * time.Second
	DefaultWriteTimeout = 30 * time.Second
	DefaultIdleTimeout  = 120 * time.Second
)

// Database defaults.
const DefaultDataDir = "/data"

// Approval defaults. Denying on timeout is the safe default: an unanswered
// request must not reach the calendar.
const (
	DefaultApprovalTimeoutMinutes = 60
	DefaultApprovalDefaultAction  = "deny"
)

// Auth defaults.
const DefaultSessionDuration = 24 * time.Hour

// Logging defaults.
const DefaultLogLevel = "info"

// Display defaults.
const DefaultTimezone = "UTC"

// Retention defaults, in days. The audit trail outlives the requests it
// describes, which is why request rows must be deletable independently.
const (
	DefaultCompletedRequestsDays = 90
	DefaultAuditLogDays          = 365
	DefaultWebhookFailuresDays   = 30
)
