// Package engine provides append-only audit logging.
package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// auditColumns is the shared SELECT list for audit queries.
const auditColumns = `id, timestamp, event_type, request_id, api_key_id, actor, details, ip_address`

// AuditLogger appends entries to the audit trail.
type AuditLogger struct {
	db *database.DB
}

// NewAuditLogger creates a new audit logger.
func NewAuditLogger(db *database.DB) *AuditLogger {
	return &AuditLogger{db: db}
}

// Entry describes a single audit event.
type Entry struct {
	EventType string
	RequestID string
	APIKeyID  string
	Actor     string
	IPAddress string
	Details   map[string]any
}

// Log appends an audit entry.
//
// The write deliberately detaches from the caller's cancellation: an approval
// that has already taken effect must still be recorded even if the client that
// triggered it hangs up mid-request. Values from the context (deadlines aside)
// are preserved.
func (a *AuditLogger) Log(ctx context.Context, entry Entry) {
	var detailsJSON []byte
	if len(entry.Details) > 0 {
		var err error
		if detailsJSON, err = json.Marshal(entry.Details); err != nil {
			util.Warn("Failed to encode audit details", "error", err, "event_type", entry.EventType)
		}
	}

	writeCtx := context.WithoutCancel(ctx)

	if _, err := a.db.ExecContext(writeCtx, `
		INSERT INTO audit_log (event_type, request_id, api_key_id, actor, details, ip_address)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
	`, entry.EventType, entry.RequestID, entry.APIKeyID, entry.Actor,
		string(detailsJSON), entry.IPAddress); err != nil {
		util.Error("Failed to write audit log", "error", err, "event_type", entry.EventType)
	}
}

// GetRecent retrieves the most recent audit entries.
func (a *AuditLogger) GetRecent(ctx context.Context, limit int) ([]database.AuditLogEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := a.db.QueryContext(ctx, `SELECT `+auditColumns+`
		FROM audit_log
		ORDER BY timestamp DESC, id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAuditRows(rows)
}

// GetByRequestID retrieves the audit trail for one request, oldest first.
func (a *AuditLogger) GetByRequestID(ctx context.Context, requestID string) ([]database.AuditLogEntry, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT `+auditColumns+`
		FROM audit_log
		WHERE request_id = ?
		ORDER BY timestamp ASC, id ASC`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAuditRows(rows)
}

// Count returns the total number of audit entries.
func (a *AuditLogger) Count(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&count)
	return count, err
}

// DeleteOlderThan removes audit entries older than the given number of days.
func (a *AuditLogger) DeleteOlderThan(ctx context.Context, days int) (int64, error) {
	result, err := a.db.ExecContext(ctx, `
		DELETE FROM audit_log
		WHERE timestamp < datetime('now', ?)
	`, fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanAuditRows(rows *sql.Rows) ([]database.AuditLogEntry, error) {
	entries := make([]database.AuditLogEntry, 0)

	for rows.Next() {
		var (
			entry       database.AuditLogEntry
			timestamp   sql.NullString
			detailsJSON sql.NullString
		)

		if err := rows.Scan(
			&entry.ID, &timestamp, &entry.EventType,
			&entry.RequestID, &entry.APIKeyID, &entry.Actor,
			&detailsJSON, &entry.IPAddress,
		); err != nil {
			return nil, err
		}

		if ts := database.NullTimeText(timestamp); ts.Valid {
			entry.Timestamp = ts.Time
		}
		if detailsJSON.Valid && detailsJSON.String != "" {
			entry.Details = json.RawMessage(detailsJSON.String)
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}
