// Package config provides default values for configuration.
package config

import "time"

// Server defaults
const (
	DefaultHost         = "0.0.0.0"
	DefaultPort         = 8080
	DefaultBaseURL      = "http://localhost:8080"
	DefaultReadTimeout  = 30 * time.Second
	DefaultWriteTimeout = 30 * time.Second
)

// Database defaults
const (
	DefaultDataDir       = "/data"
	DefaultBusyTimeoutMs = 5000
)

// Approval defaults
const (
	DefaultApprovalTimeoutMinutes = 60
	DefaultApprovalDefaultAction  = "deny"
)

// Auth defaults
const (
	DefaultSessionDuration = 24 * time.Hour
)

// Logging defaults
const (
	DefaultLogLevel = "info"
)

// Display defaults
const (
	DefaultTimezone = "America/New_York"
)

// Retention defaults
const (
	DefaultCompletedRequestsDays = 90
	DefaultAuditLogDays          = 365
	DefaultWebhookFailuresDays   = 30
)
