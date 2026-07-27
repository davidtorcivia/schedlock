package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/dtorcivia/schedlock/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

// TestApprovalPINSurvivesUnrelatedSettingsSave is the regression test for a
// silent security downgrade: the settings form built a fresh settings document
// from its own fields and saved it wholesale, so every save of an unrelated
// setting (log level, timezone, retention) erased the approval PIN. The PIN
// stopped being required and nothing said so.
func TestApprovalPINSurvivesUnrelatedSettingsSave(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := stored.SetApprovalPIN("246810"); err != nil {
		t.Fatalf("SetApprovalPIN failed: %v", err)
	}
	if err := store.Save(ctx, stored); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Simulate the settings form: load, change one unrelated field, save.
	current, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	current.Logging = &LoggingSettings{Level: "debug", Format: "text"}
	current.Approval = &ApprovalSettings{TimeoutMinutes: 30, DefaultAction: "deny"}
	current.Retention = &RetentionSettings{
		CompletedRequestsDays: 30,
		AuditLogDays:          90,
		WebhookFailuresDays:   14,
	}
	if err := store.Save(ctx, current); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	hasPIN, err := store.HasApprovalPIN(ctx)
	if err != nil {
		t.Fatalf("HasApprovalPIN failed: %v", err)
	}
	if !hasPIN {
		t.Fatal("the approval PIN was lost when unrelated settings were saved")
	}

	valid, err := store.VerifyApprovalPIN(ctx, "246810")
	if err != nil {
		t.Fatalf("VerifyApprovalPIN failed: %v", err)
	}
	if !valid {
		t.Error("the stored PIN no longer verifies")
	}
}

func TestApprovalPINVerification(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// With no PIN configured, verification passes: the PIN is optional.
	valid, err := store.VerifyApprovalPIN(ctx, "")
	if err != nil {
		t.Fatalf("VerifyApprovalPIN failed: %v", err)
	}
	if !valid {
		t.Error("expected verification to pass when no PIN is configured")
	}

	stored, _ := store.Load(ctx)
	if err := stored.SetApprovalPIN("1234"); err != nil {
		t.Fatalf("SetApprovalPIN failed: %v", err)
	}
	if err := store.Save(ctx, stored); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	for _, tc := range []struct {
		pin  string
		want bool
	}{
		{"1234", true},
		{"4321", false},
		{"", false},
		{"12345", false},
	} {
		valid, err := store.VerifyApprovalPIN(ctx, tc.pin)
		if err != nil {
			t.Fatalf("VerifyApprovalPIN(%q) failed: %v", tc.pin, err)
		}
		if valid != tc.want {
			t.Errorf("VerifyApprovalPIN(%q) = %v, want %v", tc.pin, valid, tc.want)
		}
	}

	// Clearing removes the requirement.
	stored, _ = store.Load(ctx)
	stored.ClearApprovalPIN()
	if err := store.Save(ctx, stored); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if hasPIN, _ := store.HasApprovalPIN(ctx); hasPIN {
		t.Error("expected the PIN requirement to be cleared")
	}
}

func TestValidatePIN(t *testing.T) {
	valid := []string{"1234", "12345678", "0000"}
	for _, pin := range valid {
		if err := ValidatePIN(pin); err != nil {
			t.Errorf("ValidatePIN(%q) rejected a valid PIN: %v", pin, err)
		}
	}

	invalid := []string{"", "123", "123456789", "12a4", "12 4", "１２３４"}
	for _, pin := range invalid {
		if err := ValidatePIN(pin); err == nil {
			t.Errorf("ValidatePIN(%q) accepted an invalid PIN", pin)
		}
	}
}

// TestPINIsNotStoredInPlaintext guards the stored representation: a settings
// row is readable by anyone with database access, so the PIN must only ever be
// present as a hash.
func TestPINIsNotStoredInPlaintext(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	stored, _ := store.Load(ctx)
	if err := stored.SetApprovalPIN("13579"); err != nil {
		t.Fatalf("SetApprovalPIN failed: %v", err)
	}
	if err := store.Save(ctx, stored); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	var raw string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw); err != nil {
		t.Fatalf("failed to read the settings row: %v", err)
	}
	if strings.Contains(raw, "13579") {
		t.Error("the PIN appears in the stored settings in plaintext")
	}
}
