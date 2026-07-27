package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// vacuumInterval is how often the database is compacted.
const vacuumInterval = 24 * time.Hour

// CleanupWorker enforces data retention and reclaims space.
type CleanupWorker struct {
	db       *database.DB
	config   *config.RetentionConfig
	interval time.Duration
}

// NewCleanupWorker creates a cleanup worker.
func NewCleanupWorker(db *database.DB, cfg *config.RetentionConfig) *CleanupWorker {
	return &CleanupWorker{db: db, config: cfg, interval: time.Hour}
}

// Start runs cleanup until the context is cancelled.
func (w *CleanupWorker) Start(ctx context.Context) {
	util.Info("Starting cleanup worker",
		"interval", w.interval,
		"enabled", w.config.Enabled,
		"request_days", w.config.CompletedRequestsDays,
		"audit_days", w.config.AuditLogDays,
		"webhook_days", w.config.WebhookFailuresDays,
	)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

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

// task is one retention sweep.
type task struct {
	name  string
	query string
	args  []any
}

// runCleanup performs every retention sweep, then compacts if due.
//
// Sessions, decision tokens, and idempotency keys are pruned on their own
// schedule regardless of the retention setting: they are short-lived operational
// state, not user-visible history, and letting them accumulate indefinitely is
// never the intent of disabling retention.
func (w *CleanupWorker) runCleanup(ctx context.Context) {
	always := []task{
		{"expired sessions", `DELETE FROM sessions WHERE expires_at < datetime('now')`, nil},
		{"expired decision tokens", `DELETE FROM decision_tokens WHERE expires_at < datetime('now')`, nil},
		{"stale idempotency keys", `DELETE FROM idempotency_keys WHERE created_at < datetime('now', '-24 hours')`, nil},
	}
	for _, t := range always {
		w.run(ctx, t)
	}

	if w.config == nil || !w.config.Enabled {
		util.Debug("Retention cleanup disabled")
		return
	}

	// Requests are deleted last among the history sweeps so that the rows that
	// cascade from them (notifications, tokens, idempotency keys) are already
	// mostly gone, and so audit entries are pruned on their own longer clock.
	retention := []task{
		{"old audit entries",
			`DELETE FROM audit_log WHERE timestamp < datetime('now', ?)`,
			[]any{days(w.config.AuditLogDays)}},
		{"old notification logs",
			`DELETE FROM notification_log WHERE sent_at < datetime('now', ?)`,
			[]any{days(w.config.CompletedRequestsDays)}},
		{"resolved webhook failures",
			`DELETE FROM webhook_failures WHERE created_at < datetime('now', ?)`,
			[]any{days(w.config.WebhookFailuresDays)}},
		{"completed requests",
			`DELETE FROM requests
			 WHERE status IN (?, ?, ?, ?, ?)
			 AND created_at < datetime('now', ?)`,
			[]any{
				database.StatusCompleted, database.StatusFailed, database.StatusExpired,
				database.StatusDenied, database.StatusCancelled,
				days(w.config.CompletedRequestsDays),
			}},
	}
	for _, t := range retention {
		w.run(ctx, t)
	}

	w.maybeVacuum(ctx)
}

func (w *CleanupWorker) run(ctx context.Context, t task) {
	result, err := w.db.ExecContext(ctx, t.query, t.args...)
	if err != nil {
		util.Error("Cleanup task failed", "task", t.name, "error", err)
		return
	}
	if rows, err := result.RowsAffected(); err == nil && rows > 0 {
		util.Info("Cleaned up "+t.name, "count", rows)
	}
}

// maybeVacuum compacts the database at most once a day.
func (w *CleanupWorker) maybeVacuum(ctx context.Context) {
	var lastVacuum string
	err := w.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'last_vacuum'`).Scan(&lastVacuum)
	if err == nil {
		if last, parseErr := time.Parse(time.RFC3339, lastVacuum); parseErr == nil {
			if time.Since(last) < vacuumInterval {
				return
			}
		}
	}

	util.Info("Running database VACUUM")
	if err := w.db.Vacuum(ctx); err != nil {
		util.Error("Failed to VACUUM database", "error", err)
		return
	}

	if _, err := w.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ('last_vacuum', ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
	`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		util.Error("Failed to record vacuum time", "error", err)
	}
}

// days renders a retention window as a SQLite date modifier, clamping
// nonsensical values so a misconfiguration cannot delete live data.
func days(n int) string {
	if n < 1 {
		n = 1
	}
	return fmt.Sprintf("-%d days", n)
}
