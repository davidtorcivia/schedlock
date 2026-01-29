package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
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
}

type ApprovalSettings struct {
	TimeoutMinutes int    `json:"timeout_minutes"`
	DefaultAction  string `json:"default_action"`
}

type RetentionSettings struct {
	CompletedRequestsDays int `json:"completed_requests_days"`
	AuditLogDays          int `json:"audit_log_days"`
	WebhookFailuresDays   int `json:"webhook_failures_days"`
}

type LoggingSettings struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type DisplaySettings struct {
	Timezone string `json:"timezone"`
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

	return nil
}
