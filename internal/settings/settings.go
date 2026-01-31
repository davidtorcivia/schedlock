package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
	"golang.org/x/crypto/bcrypt"
)

const settingsKey = "runtime_settings"

// Store manages runtime settings stored in the database.
type Store struct {
	db *database.DB
}

// NewStore creates a new runtime settings store.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// RuntimeSettings represents settings that can be changed at runtime.
type RuntimeSettings struct {
	Approval  *ApprovalSettings  `json:"approval,omitempty"`
	Retention *RetentionSettings `json:"retention,omitempty"`
	Logging   *LoggingSettings   `json:"logging,omitempty"`
	Display   *DisplaySettings   `json:"display,omitempty"`
	Server    *ServerSettings    `json:"server,omitempty"`
	Security  *SecuritySettings  `json:"security,omitempty"`
}

type ApprovalSettings struct {
	TimeoutMinutes int    `json:"timeout_minutes"`
	DefaultAction  string `json:"default_action"`
}

type RetentionSettings struct {
	Enabled               *bool `json:"enabled,omitempty"`
	CompletedRequestsDays int   `json:"completed_requests_days"`
	AuditLogDays          int   `json:"audit_log_days"`
	WebhookFailuresDays   int   `json:"webhook_failures_days"`
}

type LoggingSettings struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type DisplaySettings struct {
	Timezone       string `json:"timezone"`
	DateFormat     string `json:"date_format"`
	TimeFormat     string `json:"time_format"`
	DatetimeFormat string `json:"datetime_format"`
}

// ServerSettings holds server configuration.
type ServerSettings struct {
	BaseURL string `json:"base_url,omitempty"`
}

// SecuritySettings holds security configuration.
type SecuritySettings struct {
	ApprovalPINHash string `json:"approval_pin_hash,omitempty"` // bcrypt hash of the approval PIN
}

// Load retrieves runtime settings from the database.
func (s *Store) Load(ctx context.Context) (*RuntimeSettings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return &RuntimeSettings{}, nil
		}
		return nil, err
	}

	if raw == "" {
		return &RuntimeSettings{}, nil
	}

	var settings RuntimeSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("invalid runtime settings: %w", err)
	}

	return &settings, nil
}

// Save stores runtime settings in the database.
func (s *Store) Save(ctx context.Context, settings *RuntimeSettings) error {
	if settings == nil {
		return nil
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to serialize settings: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
	`, settingsKey, string(data))
	return err
}

// SetApprovalPIN sets the approval PIN (hashes and stores it).
// Pass empty string to disable PIN requirement.
func (s *Store) SetApprovalPIN(ctx context.Context, pin string) error {
	settings, err := s.Load(ctx)
	if err != nil {
		return err
	}

	if settings.Security == nil {
		settings.Security = &SecuritySettings{}
	}

	if pin == "" {
		settings.Security.ApprovalPINHash = ""
	} else {
		hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash PIN: %w", err)
		}
		settings.Security.ApprovalPINHash = string(hash)
	}

	return s.Save(ctx, settings)
}

// VerifyApprovalPIN checks if the provided PIN matches the stored hash.
// Returns true if PIN is correct, or if no PIN is configured.
func (s *Store) VerifyApprovalPIN(ctx context.Context, pin string) (bool, error) {
	settings, err := s.Load(ctx)
	if err != nil {
		return false, err
	}

	// No PIN configured - always valid
	if settings.Security == nil || settings.Security.ApprovalPINHash == "" {
		return true, nil
	}

	// PIN required but not provided
	if pin == "" {
		return false, nil
	}

	// Verify PIN
	err = bcrypt.CompareHashAndPassword([]byte(settings.Security.ApprovalPINHash), []byte(pin))
	return err == nil, nil
}

// HasApprovalPIN returns true if an approval PIN is configured.
func (s *Store) HasApprovalPIN(ctx context.Context) (bool, error) {
	settings, err := s.Load(ctx)
	if err != nil {
		return false, err
	}
	return settings.Security != nil && settings.Security.ApprovalPINHash != "", nil
}

// Validate ensures runtime settings are valid.
func (s *RuntimeSettings) Validate() error {
	if s == nil {
		return nil
	}
	if s.Approval != nil {
		if s.Approval.TimeoutMinutes < 1 || s.Approval.TimeoutMinutes > 1440 {
			return fmt.Errorf("approval timeout must be between 1 and 1440 minutes")
		}
		if s.Approval.DefaultAction != "" && s.Approval.DefaultAction != "approve" && s.Approval.DefaultAction != "deny" {
			return fmt.Errorf("approval default action must be approve or deny")
		}
	}
	if s.Retention != nil {
		if s.Retention.CompletedRequestsDays < 1 || s.Retention.CompletedRequestsDays > 3650 {
			return fmt.Errorf("completed request retention must be between 1 and 3650 days")
		}
		if s.Retention.AuditLogDays < 1 || s.Retention.AuditLogDays > 3650 {
			return fmt.Errorf("audit log retention must be between 1 and 3650 days")
		}
		if s.Retention.WebhookFailuresDays < 1 || s.Retention.WebhookFailuresDays > 3650 {
			return fmt.Errorf("webhook failures retention must be between 1 and 3650 days")
		}
	}
	if s.Logging != nil {
		if s.Logging.Level != "" {
			if s.Logging.Level != "debug" && s.Logging.Level != "info" && s.Logging.Level != "warn" && s.Logging.Level != "error" {
				return fmt.Errorf("invalid log level")
			}
		}
		if s.Logging.Format != "" {
			if s.Logging.Format != "json" && s.Logging.Format != "text" {
				return fmt.Errorf("invalid log format")
			}
		}
	}
	if s.Display != nil && s.Display.Timezone != "" {
		if _, err := util.NewDisplayFormatter(s.Display.Timezone, "", "", ""); err != nil {
			return fmt.Errorf("invalid display timezone: %w", err)
		}
	}
	if s.Server != nil && s.Server.BaseURL != "" {
		if !strings.HasPrefix(s.Server.BaseURL, "http://") && !strings.HasPrefix(s.Server.BaseURL, "https://") {
			return fmt.Errorf("base_url must start with http:// or https://")
		}
		// Remove trailing slash for consistency
		s.Server.BaseURL = strings.TrimSuffix(s.Server.BaseURL, "/")
	}
	return nil
}

// ApplyTo applies runtime settings to the provided config.
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
	if s.Display != nil && s.Display.Timezone != "" {
		cfg.Display.Timezone = s.Display.Timezone
	}
	if s.Display != nil {
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
		// Update OAuth redirect URI to match
		cfg.Google.RedirectURI = s.Server.BaseURL + "/oauth/callback"
	}

	return nil
}
