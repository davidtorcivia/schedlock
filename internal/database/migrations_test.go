package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}
	var first int
	if err := db.QueryRow("SELECT MAX(version) FROM migrations").Scan(&first); err != nil {
		t.Fatalf("reading version: %v", err)
	}
	db.Close()

	// Re-opening must not re-run migrations or fail on existing objects.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer db2.Close()

	var second int
	if err := db2.QueryRow("SELECT MAX(version) FROM migrations").Scan(&second); err != nil {
		t.Fatalf("reading version: %v", err)
	}
	if first != second {
		t.Errorf("migration version changed on reopen: %d -> %d", first, second)
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count); err != nil {
		t.Fatalf("counting migrations: %v", err)
	}
	if count != len(allMigrations()) {
		t.Errorf("expected %d migration rows, got %d", len(allMigrations()), count)
	}
}

func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	db := openTestDB(t)

	// Keep several connections checked out at once so the assertions cannot
	// all land on the same pooled connection.
	const conns = 4
	var held []*sql.Rows
	for i := 0; i < conns; i++ {
		rows, err := db.Query("SELECT foreign_keys FROM pragma_foreign_keys")
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if !rows.Next() {
			t.Fatalf("no pragma row on connection %d", i)
		}
		var fk int
		if err := rows.Scan(&fk); err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("connection %d has foreign_keys=%d, want 1", i, fk)
		}
		held = append(held, rows)
	}
	for _, rows := range held {
		rows.Close()
	}

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
}

// TestRetentionDeleteOfRequestsSucceeds guards the schema defect where every
// child table referenced requests(id) without an ON DELETE action, so purging
// an old request failed as long as any audit row still pointed at it.
func TestRetentionDeleteOfRequestsSucceeds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	mustExec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_1', 'key_1', 'create_event', 'completed', '{}', datetime('now'))`)
	mustExec(t, db, `INSERT INTO audit_log (event_type, request_id, api_key_id, actor)
		VALUES ('request_created', 'req_1', 'key_1', 'api')`)
	mustExec(t, db, `INSERT INTO notification_log (request_id, provider, status)
		VALUES ('req_1', 'webhook', 'sent')`)
	mustExec(t, db, `INSERT INTO decision_tokens (token_hash, request_id, allowed_actions, expires_at)
		VALUES ('hash1', 'req_1', '["approve"]', datetime('now'))`)
	mustExec(t, db, `INSERT INTO idempotency_keys (api_key_id, idempotency_key, request_id)
		VALUES ('key_1', 'idem-1', 'req_1')`)

	if _, err := db.ExecContext(ctx, `DELETE FROM requests WHERE id = 'req_1'`); err != nil {
		t.Fatalf("deleting a retained request failed: %v", err)
	}

	// The audit trail outlives the request it describes.
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE request_id IS NULL`).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("expected the audit row to survive with a NULL request_id, got %d rows", auditCount)
	}

	// Transient child rows are removed with the request.
	for _, table := range []string{"notification_log", "decision_tokens", "idempotency_keys"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s should cascade with the request, got %d rows", table, n)
		}
	}
}

// TestNotificationLogAcceptsWebhookProvider guards the CHECK constraint that
// silently rejected every generic-webhook delivery record.
func TestNotificationLogAcceptsWebhookProvider(t *testing.T) {
	db := openTestDB(t)

	mustExec(t, db, `INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	mustExec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_1', 'key_1', 'create_event', 'pending_approval', '{}', datetime('now'))`)

	for _, provider := range []string{"ntfy", "pushover", "telegram", "webhook"} {
		if _, err := db.Exec(`INSERT INTO notification_log (request_id, provider, status)
			VALUES ('req_1', ?, 'sent')`, provider); err != nil {
			t.Errorf("provider %q rejected by notification_log: %v", provider, err)
		}
	}

	// Status remains constrained.
	if _, err := db.Exec(`INSERT INTO notification_log (request_id, provider, status)
		VALUES ('req_1', 'ntfy', 'bogus')`); err == nil {
		t.Error("expected an invalid notification status to be rejected")
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(`INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_x', 'key_missing', 'create_event', 'pending_approval', '{}', datetime('now'))`); err == nil {
		t.Error("expected a foreign key violation for an unknown api_key_id")
	}
}

func mustExec(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
