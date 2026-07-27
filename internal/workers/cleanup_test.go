package workers

import (
	"context"
	"testing"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
)

func newCleanupFixture(t *testing.T, cfg *config.RetentionConfig) (*CleanupWorker, *database.DB) {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return NewCleanupWorker(db, cfg), db
}

func seedOldRequest(t *testing.T, db *database.DB, id string) {
	t.Helper()

	exec(t, db, `INSERT OR IGNORE INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	exec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at, created_at)
		VALUES (?, 'key_1', 'create_event', 'completed', '{}', datetime('now', '-200 days'), datetime('now', '-200 days'))`, id)
	exec(t, db, `INSERT INTO audit_log (event_type, request_id, api_key_id, actor, timestamp)
		VALUES ('request_created', ?, 'key_1', 'api', datetime('now', '-200 days'))`, id)
	exec(t, db, `INSERT INTO notification_log (request_id, provider, status, sent_at)
		VALUES (?, 'ntfy', 'sent', datetime('now', '-200 days'))`, id)
	exec(t, db, `INSERT INTO decision_tokens (token_hash, request_id, allowed_actions, expires_at)
		VALUES (?, ?, '["approve"]', datetime('now', '-199 days'))`, "hash_"+id, id)
}

func exec(t *testing.T, db *database.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec failed: %v", err)
	}
}

func count(t *testing.T, db *database.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s failed: %v", table, err)
	}
	return n
}

// TestRetentionActuallyDeletesOldRequests is the regression test for a cleanup
// that could never succeed: child tables referenced requests(id) with no
// ON DELETE action, and audit rows are retained far longer than requests, so
// every attempt to purge an old request failed on a foreign key violation and
// the database grew without bound.
func TestRetentionActuallyDeletesOldRequests(t *testing.T) {
	worker, db := newCleanupFixture(t, &config.RetentionConfig{
		Enabled:               true,
		CompletedRequestsDays: 90,
		AuditLogDays:          365, // deliberately longer than the request window
		WebhookFailuresDays:   30,
	})

	seedOldRequest(t, db, "req_old")

	worker.runCleanup(context.Background())

	if got := count(t, db, "requests"); got != 0 {
		t.Errorf("%d old requests survived retention cleanup", got)
	}

	// The audit trail outlives the request it describes.
	if got := count(t, db, "audit_log"); got != 1 {
		t.Errorf("%d audit entries remain, want 1 (the trail must outlive the request)", got)
	}
	var orphaned int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE request_id IS NULL`).Scan(&orphaned); err != nil {
		t.Fatalf("querying audit rows failed: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("the surviving audit entry should have a NULL request reference, got %d", orphaned)
	}
}

func TestRetentionKeepsRecentData(t *testing.T) {
	worker, db := newCleanupFixture(t, &config.RetentionConfig{
		Enabled:               true,
		CompletedRequestsDays: 90,
		AuditLogDays:          365,
		WebhookFailuresDays:   30,
	})

	exec(t, db, `INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	exec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_new', 'key_1', 'create_event', 'completed', '{}', datetime('now'))`)

	worker.runCleanup(context.Background())

	if got := count(t, db, "requests"); got != 1 {
		t.Errorf("a recent request was deleted; %d remain", got)
	}
}

// TestDisabledRetentionStillPrunesTransientState checks that switching off
// history retention does not also switch off cleanup of expired sessions and
// spent tokens, which are operational state rather than history.
func TestDisabledRetentionStillPrunesTransientState(t *testing.T) {
	worker, db := newCleanupFixture(t, &config.RetentionConfig{Enabled: false})

	seedOldRequest(t, db, "req_old")
	exec(t, db, `INSERT INTO sessions (id, expires_at, csrf_token)
		VALUES ('sess_old', datetime('now', '-1 day'), 'tok')`)

	worker.runCleanup(context.Background())

	if got := count(t, db, "sessions"); got != 0 {
		t.Errorf("%d expired sessions survived", got)
	}
	if got := count(t, db, "decision_tokens"); got != 0 {
		t.Errorf("%d expired decision tokens survived", got)
	}
	// History is preserved, since retention is off.
	if got := count(t, db, "requests"); got != 1 {
		t.Errorf("retention is disabled but %d requests were deleted", 1-got)
	}
}

// TestRetentionWindowIsClamped guards against a misconfiguration deleting live
// data: a zero or negative window must not become "delete everything".
func TestRetentionWindowIsClamped(t *testing.T) {
	worker, db := newCleanupFixture(t, &config.RetentionConfig{
		Enabled:               true,
		CompletedRequestsDays: 0,
		AuditLogDays:          -5,
		WebhookFailuresDays:   0,
	})

	exec(t, db, `INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	exec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_today', 'key_1', 'create_event', 'completed', '{}', datetime('now'))`)

	worker.runCleanup(context.Background())

	if got := count(t, db, "requests"); got != 1 {
		t.Error("a request created today was deleted by a zero-day retention window")
	}
}
