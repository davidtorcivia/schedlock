// Package database handles database migrations.
package database

import (
	"fmt"
)

// migrate runs all database migrations.
func (db *DB) migrate() error {
	// Create migrations table if not exists
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Run migrations
	migrations := getAllMigrations()
	for _, m := range migrations {
		if m.version > currentVersion {
			if err := db.runMigration(m); err != nil {
				return fmt.Errorf("migration %d failed: %w", m.version, err)
			}
		}
	}

	return nil
}

type migration struct {
	version int
	sql     string
}

func (db *DB) runMigration(m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	if _, err := tx.Exec("INSERT INTO migrations (version) VALUES (?)", m.version); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

func getAllMigrations() []migration {
	return []migration{
		{
			version: 1,
			sql:     migration001InitialSchema,
		},
		{
			version: 2,
			sql:     migration002NotificationCredentials,
		},
	}
}

const migration002NotificationCredentials = `
-- Notification credentials table
-- Stores encrypted credentials for notification providers
CREATE TABLE IF NOT EXISTS notification_credentials (
    provider TEXT PRIMARY KEY,              -- 'ntfy', 'pushover', 'telegram'
    enabled INTEGER NOT NULL DEFAULT 0,     -- 1 = enabled, 0 = disabled
    credentials_enc BLOB,                   -- AES-256-GCM encrypted JSON
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
`

const migration001InitialSchema = `
-- API Keys table
-- Stores API keys with HMAC-SHA256 hashed values
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,                    -- "key_" + nanoid(16)
    key_hash TEXT UNIQUE NOT NULL,          -- HMAC-SHA256(server_secret, full_key)
    key_prefix TEXT NOT NULL,               -- First 12 chars for display
    name TEXT NOT NULL,                     -- Human-readable identifier
    tier TEXT NOT NULL CHECK (tier IN ('read', 'write', 'admin')),
    constraints TEXT,                       -- JSON: per-key policy constraints
    created_at TEXT DEFAULT (datetime('now')),
    last_used_at TEXT,
    expires_at TEXT,                        -- NULL = never expires
    revoked_at TEXT,
    rate_limit_override INTEGER,            -- NULL = use tier default
    metadata TEXT                           -- JSON for future extensions
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_tier ON api_keys(tier) WHERE revoked_at IS NULL;


-- Requests table
-- Stores pending and completed calendar operation requests
CREATE TABLE IF NOT EXISTS requests (
    id TEXT PRIMARY KEY,                    -- "req_" + nanoid(16)
    api_key_id TEXT NOT NULL REFERENCES api_keys(id),
    operation TEXT NOT NULL CHECK (operation IN (
        'create_event', 'update_event', 'delete_event'
    )),
    status TEXT NOT NULL DEFAULT 'pending_approval' CHECK (status IN (
        'pending_approval', 'change_requested', 'approved', 'denied', 'expired',
        'cancelled', 'executing', 'completed', 'failed'
    )),
    payload TEXT NOT NULL,                  -- JSON: original request body
    result TEXT,                            -- JSON: Google API response
    error TEXT,                             -- Error message on failure
    suggestion_text TEXT,                   -- User's suggested changes (if change_requested)
    suggestion_at TEXT,                     -- When suggestion was submitted
    suggestion_by TEXT,                     -- 'ntfy', 'pushover', 'telegram', 'web_ui'
    created_at TEXT DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    decided_at TEXT,
    decided_by TEXT,                        -- 'ntfy', 'pushover', 'telegram', 'web_ui', 'timeout'
    executed_at TEXT,
    retry_count INTEGER DEFAULT 0,
    webhook_notified_at TEXT               -- When Moltbot webhook was sent
);

CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status);
CREATE INDEX IF NOT EXISTS idx_requests_pending ON requests(expires_at)
    WHERE status = 'pending_approval';
CREATE INDEX IF NOT EXISTS idx_requests_api_key ON requests(api_key_id);
CREATE INDEX IF NOT EXISTS idx_requests_created ON requests(created_at);


-- Audit Log table
-- Append-only log of all operations
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT DEFAULT (datetime('now')),
    event_type TEXT NOT NULL,               -- See event types below
    request_id TEXT REFERENCES requests(id),
    api_key_id TEXT REFERENCES api_keys(id),
    actor TEXT,                             -- Who/what triggered event
    details TEXT,                           -- JSON: event-specific data
    ip_address TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_log(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_request ON audit_log(request_id);

-- Event types:
-- api_key_created, api_key_revoked, api_key_used
-- request_created, request_approved, request_denied, request_expired
-- request_change_requested, request_cancelled
-- request_executing, request_completed, request_failed
-- notification_sent, notification_failed, callback_received
-- settings_changed, oauth_connected, oauth_refreshed, oauth_failed
-- login_success, login_failed, session_created, session_expired


-- Notification Log table
-- Tracks notification delivery with message IDs for reply matching
CREATE TABLE IF NOT EXISTS notification_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL REFERENCES requests(id),
    provider TEXT NOT NULL CHECK (provider IN ('ntfy', 'pushover', 'telegram')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'callback_received')),
    sent_at TEXT DEFAULT (datetime('now')),
    callback_at TEXT,
    error TEXT,
    response TEXT,                          -- JSON: provider response
    message_id TEXT                         -- Provider's message ID (critical for Telegram replies)
);

CREATE INDEX IF NOT EXISTS idx_notification_request ON notification_log(request_id);
CREATE INDEX IF NOT EXISTS idx_notification_provider ON notification_log(provider, status);
CREATE INDEX IF NOT EXISTS idx_notification_message_id ON notification_log(provider, message_id);


-- OAuth Tokens table
-- Stores encrypted Google OAuth refresh tokens
CREATE TABLE IF NOT EXISTS oauth_tokens (
    id TEXT PRIMARY KEY DEFAULT 'primary',
    refresh_token_enc BLOB NOT NULL,        -- AES-256-GCM encrypted
    scopes TEXT,                            -- Space-separated scope list
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);


-- Settings table
-- Key-value store for runtime configuration
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,                    -- JSON
    updated_at TEXT DEFAULT (datetime('now'))
);


-- Sessions table
-- Web UI session management
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,                    -- Secure random 32 bytes, base64
    created_at TEXT DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    last_activity TEXT,
    ip_address TEXT,
    user_agent TEXT,
    csrf_token TEXT NOT NULL                -- CSRF protection token
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);


-- Decision Tokens table
-- Single-use tokens for approval callbacks
CREATE TABLE IF NOT EXISTS decision_tokens (
    token_hash TEXT PRIMARY KEY,            -- SHA-256 of token
    request_id TEXT NOT NULL REFERENCES requests(id),
    allowed_actions TEXT NOT NULL,          -- JSON array: ["approve", "deny", "suggest"]
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    consumed_action TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_decision_tokens_request ON decision_tokens(request_id);
CREATE INDEX IF NOT EXISTS idx_decision_tokens_expires ON decision_tokens(expires_at)
    WHERE consumed_at IS NULL;


-- Idempotency Keys table
-- Prevents duplicate requests from creating duplicate events
CREATE TABLE IF NOT EXISTS idempotency_keys (
    api_key_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_id TEXT NOT NULL REFERENCES requests(id),
    created_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY (api_key_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_idempotency_created ON idempotency_keys(created_at);


-- Webhook Failures table
-- Tracks failed Moltbot webhook deliveries for retry/observability
CREATE TABLE IF NOT EXISTS webhook_failures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id TEXT NOT NULL,               -- Unique ID for this delivery attempt
    request_id TEXT NOT NULL,
    status TEXT NOT NULL,                   -- Request status that triggered webhook
    payload TEXT NOT NULL,                  -- JSON payload that failed
    error TEXT,
    attempts INTEGER DEFAULT 1,
    created_at TEXT DEFAULT (datetime('now')),
    resolved_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_webhook_failures_request ON webhook_failures(request_id);
CREATE INDEX IF NOT EXISTS idx_webhook_failures_resolved ON webhook_failures(resolved_at);


-- Admin Password Hash table
-- Stores hashed admin password (Argon2id)
CREATE TABLE IF NOT EXISTS admin_auth (
    id TEXT PRIMARY KEY DEFAULT 'admin',
    password_hash TEXT NOT NULL,            -- Argon2id hash
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
`
