package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// CleanupWorker handles data retention and cleanup.
type CleanupWorker struct {
	db       *database.DB
	config   *config.RetentionConfig
	interval time.Duration
}

// NewCleanupWorker creates a new cleanup worker.
func NewCleanupWorker(db *database.DB, cfg *config.RetentionConfig) *CleanupWorker {
	return &CleanupWorker{
		db:       db,
		config:   cfg,
		interval: 1 * time.Hour, // Run every hour
	}
}

// Start starts the cleanup worker.
func (w *CleanupWorker) Start(ctx context.Context) {
	util.Info("Starting cleanup worker",
		"interval", w.interval,
		"request_days", w.config.CompletedRequestsDays,
		"audit_days", w.config.AuditLogDays,
	)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start
	w.runCleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			util.Info("Cleanup worker stopping")
			return
		case <-ticker.C:
			w.runCleanup(ctx)
		}
	}
}

// runCleanup performs all cleanup tasks.
func (w *CleanupWorker) runCleanup(ctx context.Context) {
	util.Debug("Running cleanup tasks")

	// Clean up old requests
	w.cleanupRequests(ctx)

	// Clean up old audit logs
	w.cleanupAuditLogs(ctx)

	// Clean up expired decision tokens
	w.cleanupDecisionTokens(ctx)

	// Clean up expired idempotency keys
	w.cleanupIdempotencyKeys(ctx)

	// Clean up old notification logs
	w.cleanupNotificationLogs(ctx)

	// Clean up old webhook failures
	w.cleanupWebhookFailures(ctx)

	// Clean up expired sessions
	w.cleanupSessions(ctx)

	// Run VACUUM to reclaim space (periodically)
	w.maybeVacuum(ctx)
}

// cleanupRequests removes old completed/failed/expired requests.
func (w *CleanupWorker) cleanupRequests(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM requests
		WHERE status IN (?, ?, ?, ?, ?)
		AND created_at < datetime('now', ?)
	`, database.StatusCompleted, database.StatusFailed, database.StatusExpired,
		database.StatusDenied, database.StatusCancelled,
		fmt.Sprintf("-%d days", w.config.CompletedRequestsDays))

	if err != nil {
		util.Error("Failed to cleanup requests", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up old requests", "count", rows)
	}
}

// cleanupAuditLogs removes old audit log entries.
func (w *CleanupWorker) cleanupAuditLogs(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM audit_log
		WHERE timestamp < datetime('now', ?)
	`, fmt.Sprintf("-%d days", w.config.AuditLogDays))

	if err != nil {
		util.Error("Failed to cleanup audit logs", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up old audit logs", "count", rows)
	}
}

// cleanupDecisionTokens removes expired decision tokens.
func (w *CleanupWorker) cleanupDecisionTokens(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM decision_tokens
		WHERE expires_at < datetime('now')
	`)

	if err != nil {
		util.Error("Failed to cleanup decision tokens", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up expired decision tokens", "count", rows)
	}
}

// cleanupIdempotencyKeys removes old idempotency keys (older than 24 hours).
func (w *CleanupWorker) cleanupIdempotencyKeys(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM idempotency_keys
		WHERE created_at < datetime('now', '-24 hours')
	`)

	if err != nil {
		util.Error("Failed to cleanup idempotency keys", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up old idempotency keys", "count", rows)
	}
}

// cleanupNotificationLogs removes old notification logs.
func (w *CleanupWorker) cleanupNotificationLogs(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM notification_log
		WHERE sent_at < datetime('now', ?)
	`, fmt.Sprintf("-%d days", w.config.CompletedRequestsDays))

	if err != nil {
		util.Error("Failed to cleanup notification logs", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up old notification logs", "count", rows)
	}
}

// cleanupWebhookFailures removes old webhook failure records.
func (w *CleanupWorker) cleanupWebhookFailures(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM webhook_failures
		WHERE created_at < datetime('now', '-7 days')
	`)

	if err != nil {
		util.Error("Failed to cleanup webhook failures", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up old webhook failures", "count", rows)
	}
}

// cleanupSessions removes expired sessions.
func (w *CleanupWorker) cleanupSessions(ctx context.Context) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM sessions
		WHERE expires_at < datetime('now')
	`)

	if err != nil {
		util.Error("Failed to cleanup sessions", "error", err)
		return
	}

	if rows, _ := result.RowsAffected(); rows > 0 {
		util.Info("Cleaned up expired sessions", "count", rows)
	}
}

// maybeVacuum runs VACUUM periodically (every 24 hours).
func (w *CleanupWorker) maybeVacuum(ctx context.Context) {
	// Check if we should vacuum (store last vacuum time in settings)
	var lastVacuum string
	err := w.db.QueryRowContext(ctx, `
		SELECT value FROM settings WHERE key = 'last_vacuum'
	`).Scan(&lastVacuum)

	if err == nil {
		lastTime, _ := time.Parse(time.RFC3339, lastVacuum)
		if time.Since(lastTime) < 24*time.Hour {
			return // Not time yet
		}
	}

	// Run VACUUM
	util.Info("Running database VACUUM")
	_, err = w.db.ExecContext(ctx, `VACUUM`)
	if err != nil {
		util.Error("Failed to VACUUM database", "error", err)
		return
	}

	// Update last vacuum time
	_, err = w.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO settings (key, value)
		VALUES ('last_vacuum', ?)
	`, time.Now().Format(time.RFC3339))

	if err != nil {
		util.Error("Failed to update last vacuum time", "error", err)
	}
}
