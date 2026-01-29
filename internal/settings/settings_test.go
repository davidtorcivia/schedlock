package settings

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
)

func TestRuntimeSettingsApplyTo(t *testing.T) {
	cfg := &config.Config{
		Approval: config.ApprovalConfig{
			TimeoutMinutes: 30,
			DefaultAction:  "deny",
		},
		Retention: config.RetentionConfig{
			CompletedRequestsDays: 10,
			AuditLogDays:          20,
			WebhookFailuresDays:   30,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Display: config.DisplayConfig{
			Timezone: "UTC",
		},
	}

	settings := &RuntimeSettings{
		Approval: &ApprovalSettings{
			TimeoutMinutes: 45,
			DefaultAction:  "approve",
		},
		Retention: &RetentionSettings{
			CompletedRequestsDays: 60,
			AuditLogDays:          90,
			WebhookFailuresDays:   120,
		},
		Logging: &LoggingSettings{
			Level:  "debug",
			Format: "text",
		},
		Display: &DisplaySettings{
			Timezone: "UTC",
		},
	}

	if err := settings.ApplyTo(cfg); err != nil {
		t.Fatalf("ApplyTo failed: %v", err)
	}

	if cfg.Approval.TimeoutMinutes != 45 {
		t.Fatalf("expected approval timeout 45, got %d", cfg.Approval.TimeoutMinutes)
	}
	if cfg.Approval.DefaultAction != "approve" {
		t.Fatalf("expected approval default approve, got %s", cfg.Approval.DefaultAction)
	}
	if cfg.Retention.CompletedRequestsDays != 60 {
		t.Fatalf("expected retention requests 60, got %d", cfg.Retention.CompletedRequestsDays)
	}
	if cfg.Retention.AuditLogDays != 90 {
		t.Fatalf("expected retention audit 90, got %d", cfg.Retention.AuditLogDays)
	}
	if cfg.Retention.WebhookFailuresDays != 120 {
		t.Fatalf("expected retention webhook 120, got %d", cfg.Retention.WebhookFailuresDays)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "text" {
		t.Fatalf("unexpected logging config: %s/%s", cfg.Logging.Level, cfg.Logging.Format)
	}
	if cfg.Display.Timezone != "UTC" {
		t.Fatalf("expected display timezone UTC, got %s", cfg.Display.Timezone)
	}
}

func TestRuntimeSettingsValidate(t *testing.T) {
	settings := &RuntimeSettings{
		Approval: &ApprovalSettings{
			TimeoutMinutes: 0,
		},
	}
	if err := settings.Validate(); err == nil {
		t.Fatalf("expected validation error for approval timeout")
	}

	settings = &RuntimeSettings{
		Logging: &LoggingSettings{
			Level: "verbose",
		},
	}
	if err := settings.Validate(); err == nil {
		t.Fatalf("expected validation error for log level")
	}

	settings = &RuntimeSettings{
		Display: &DisplaySettings{
			Timezone: "Not/AZone",
		},
	}
	if err := settings.Validate(); err == nil {
		t.Fatalf("expected validation error for timezone")
	}
}

func TestStoreSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "settings.db"))
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") || strings.Contains(err.Error(), "CGO_ENABLED=0") {
			t.Skipf("skipping sqlite-backed store test: %v", err)
		}
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	settings := &RuntimeSettings{
		Approval: &ApprovalSettings{
			TimeoutMinutes: 25,
			DefaultAction:  "deny",
		},
		Retention: &RetentionSettings{
			CompletedRequestsDays: 45,
			AuditLogDays:          60,
			WebhookFailuresDays:   90,
		},
		Logging: &LoggingSettings{
			Level:  "info",
			Format: "json",
		},
		Display: &DisplaySettings{
			Timezone: "UTC",
		},
	}

	if err := store.Save(context.Background(), settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}

	if !reflect.DeepEqual(settings, loaded) {
		t.Fatalf("loaded settings mismatch: %#v", loaded)
	}
}
