// Package settings stores the configuration an operator can change at runtime.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// settingsKey is the row holding the serialized runtime settings.
const settingsKey = "runtime_settings"

// PIN length bounds. A PIN is a speed bump on a link that is already secret,
// not a password, so it is kept short but throttled at the point of use.
const (
	MinPINLength = 4
	MaxPINLength = 8
)

// Store persists runtime settings.
type Store struct {
	db *database.DB
}

// NewStore creates a settings store.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// RuntimeSettings is the set of settings changeable without a restart.
type RuntimeSettings struct {
	Approval  *ApprovalSettings  `json:"approval,omitempty"`
	Retention *RetentionSettings `json:"retention,omitempty"`
	Logging   *LoggingSettings   `json:"logging,omitempty"`
	Display   *DisplaySettings   `json:"display,omitempty"`
	Server    *ServerSettings    `json:"server,omitempty"`
	Security  *SecuritySettings  `json:"security,omitempty"`
}

// ApprovalSettings controls the approval workflow.
type ApprovalSettings struct {
	TimeoutMinutes int    `json:"timeout_minutes"`
	DefaultAction  string `json:"default_action"`
}

// RetentionSettings controls how long history is kept.
type RetentionSettings struct {
	Enabled               *bool `json:"enabled,omitempty"`
	CompletedRequestsDays int   `json:"completed_requests_days"`
	AuditLogDays          int   `json:"audit_log_days"`
	WebhookFailuresDays   int   `json:"webhook_failures_days"`
}

// LoggingSettings controls log output.
type LoggingSettings struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// DisplaySettings controls how times are rendered.
type DisplaySettings struct {
	Timezone       string `json:"timezone"`
	DateFormat     string `json:"date_format"`
	TimeFormat     string `json:"time_format"`
	DatetimeFormat string `json:"datetime_format"`
}

// ServerSettings holds the public address of this server.
type ServerSettings struct {
	BaseURL string `json:"base_url,omitempty"`
}

// SecuritySettings holds security options.
type SecuritySettings struct {
	// ApprovalPINHash is a bcrypt hash; the PIN itself is never stored.
	ApprovalPINHash string `json:"approval_pin_hash,omitempty"`
}

// Load reads the stored settings, returning an empty set when none exist.
func (s *Store) Load(ctx context.Context) (*RuntimeSettings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || raw == "" {
		return &RuntimeSettings{}, nil
	}
	if err != nil {
		return nil, err
	}

	var stored RuntimeSettings
	if err := jsonUnmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("stored settings are unreadable: %w", err)
	}
	return &stored, nil
}

// Save writes the settings document.
//
// The whole document is replaced, so callers must load, modify, and save rather
// than constructing a fresh document: building one from a single form silently
// drops every field that form does not own.
func (s *Store) Save(ctx context.Context, settings *RuntimeSettings) error {
	if settings == nil {
		return nil
	}

	data, err := jsonMarshal(settings)
	if err != nil {
		return fmt.Errorf("failed to serialize settings: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
	`, settingsKey, data)
	return err
}

// SetApprovalPIN hashes and records a new approval PIN.
func (s *RuntimeSettings) SetApprovalPIN(pin string) error {
	if err := ValidatePIN(pin); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash PIN: %w", err)
	}

	if s.Security == nil {
		s.Security = &SecuritySettings{}
	}
	s.Security.ApprovalPINHash = string(hash)
	return nil
}

// ClearApprovalPIN removes the approval PIN requirement.
func (s *RuntimeSettings) ClearApprovalPIN() {
	if s.Security != nil {
		s.Security.ApprovalPINHash = ""
	}
}

// ValidatePIN checks a PIN's shape.
func ValidatePIN(pin string) error {
	if len(pin) < MinPINLength || len(pin) > MaxPINLength {
		return fmt.Errorf("the PIN must be between %d and %d digits", MinPINLength, MaxPINLength)
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return errors.New("the PIN must contain digits only")
		}
	}
	return nil
}

// VerifyApprovalPIN reports whether a submitted PIN matches.
// It returns true when no PIN is configured.
func (s *Store) VerifyApprovalPIN(ctx context.Context, pin string) (bool, error) {
	stored, err := s.Load(ctx)
	if err != nil {
		return false, err
	}

	if stored.Security == nil || stored.Security.ApprovalPINHash == "" {
		return true, nil
	}
	if pin == "" {
		return false, nil
	}

	err = bcrypt.CompareHashAndPassword([]byte(stored.Security.ApprovalPINHash), []byte(pin))
	if err != nil && !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, fmt.Errorf("stored PIN is unusable: %w", err)
	}
	return err == nil, nil
}

// HasApprovalPIN reports whether an approval PIN is configured.
func (s *Store) HasApprovalPIN(ctx context.Context) (bool, error) {
	stored, err := s.Load(ctx)
	if err != nil {
		return false, err
	}
	return stored.Security != nil && stored.Security.ApprovalPINHash != "", nil
}

// Validate checks the settings and normalizes the base URL.
func (s *RuntimeSettings) Validate() error {
	if s == nil {
		return nil
	}

	if s.Approval != nil {
		if s.Approval.TimeoutMinutes < 1 || s.Approval.TimeoutMinutes > 1440 {
			return errors.New("the approval timeout must be between 1 and 1440 minutes")
		}
		switch s.Approval.DefaultAction {
		case "", "approve", "deny":
		default:
			return errors.New("the default action on timeout must be approve or deny")
		}
	}

	if s.Retention != nil {
		for label, days := range map[string]int{
			"completed request retention": s.Retention.CompletedRequestsDays,
			"audit log retention":         s.Retention.AuditLogDays,
			"webhook failure retention":   s.Retention.WebhookFailuresDays,
		} {
			if days < 1 || days > 3650 {
				return fmt.Errorf("%s must be between 1 and 3650 days", label)
			}
		}
	}

	if s.Logging != nil {
		switch s.Logging.Level {
		case "", "debug", "info", "warn", "error":
		default:
			return errors.New("the log level must be debug, info, warn, or error")
		}
		switch s.Logging.Format {
		case "", "json", "text":
		default:
			return errors.New("the log format must be json or text")
		}
	}

	if s.Display != nil && s.Display.Timezone != "" {
		if _, err := util.NewDisplayFormatter(s.Display.Timezone, "", "", ""); err != nil {
			return fmt.Errorf("unknown timezone %q", s.Display.Timezone)
		}
	}

	if s.Server != nil && s.Server.BaseURL != "" {
		if !strings.HasPrefix(s.Server.BaseURL, "http://") && !strings.HasPrefix(s.Server.BaseURL, "https://") {
			return errors.New("the base URL must start with http:// or https://")
		}
		s.Server.BaseURL = strings.TrimRight(s.Server.BaseURL, "/")
	}

	return nil
}

// ApplyTo overlays the settings onto a configuration.
func (s *RuntimeSettings) ApplyTo(cfg *config.Config) error {
	if cfg == nil || s == nil {
		return nil
	}
	if err := s.Validate(); err != nil {
		return err
	}

	if s.Approval != nil {
		if s.Approval.TimeoutMinutes > 0 {
			cfg.Approval.TimeoutMinutes = s.Approval.TimeoutMinutes
		}
		if s.Approval.DefaultAction != "" {
			cfg.Approval.DefaultAction = s.Approval.DefaultAction
		}
	}

	if s.Retention != nil {
		if s.Retention.Enabled != nil {
			cfg.Retention.Enabled = *s.Retention.Enabled
		}
		if s.Retention.CompletedRequestsDays > 0 {
			cfg.Retention.CompletedRequestsDays = s.Retention.CompletedRequestsDays
		}
		if s.Retention.AuditLogDays > 0 {
			cfg.Retention.AuditLogDays = s.Retention.AuditLogDays
		}
		if s.Retention.WebhookFailuresDays > 0 {
			cfg.Retention.WebhookFailuresDays = s.Retention.WebhookFailuresDays
		}
	}

	if s.Logging != nil {
		if s.Logging.Level != "" {
			cfg.Logging.Level = s.Logging.Level
		}
		if s.Logging.Format != "" {
			cfg.Logging.Format = s.Logging.Format
		}
	}

	if s.Display != nil {
		if s.Display.Timezone != "" {
			cfg.Display.Timezone = s.Display.Timezone
		}
		if s.Display.DateFormat != "" {
			cfg.Display.DateFormat = s.Display.DateFormat
		}
		if s.Display.TimeFormat != "" {
			cfg.Display.TimeFormat = s.Display.TimeFormat
		}
		if s.Display.DatetimeFormat != "" {
			cfg.Display.DatetimeFormat = s.Display.DatetimeFormat
		}
	}

	if s.Server != nil && s.Server.BaseURL != "" {
		cfg.Server.BaseURL = s.Server.BaseURL
		cfg.Google.RedirectURI = s.Server.BaseURL + "/oauth/callback"
	}

	return nil
}
