// Package engine provides audit logging functionality.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// AuditLogger handles audit log entries.
type AuditLogger struct {
	db *database.DB
}

// NewAuditLogger creates a new audit logger.
func NewAuditLogger(db *database.DB) *AuditLogger {
	return &AuditLogger{db: db}
}

// Log records an audit event.
func (a *AuditLogger) Log(ctx context.Context, eventType, requestID, apiKeyID, actor string, details map[string]interface{}) {
	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	_, err := a.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_type, request_id, api_key_id, actor, details)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
	`, eventType, requestID, apiKeyID, actor, string(detailsJSON))

	if err != nil {
		util.Error("Failed to write audit log", "error", err, "event_type", eventType)
	}
}

// LogWithIP records an audit event with IP address.
func (a *AuditLogger) LogWithIP(ctx context.Context, eventType, requestID, apiKeyID, actor, ipAddress string, details map[string]interface{}) {
	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	_, err := a.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_type, request_id, api_key_id, actor, details, ip_address)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
	`, eventType, requestID, apiKeyID, actor, string(detailsJSON), ipAddress)

	if err != nil {
		util.Error("Failed to write audit log", "error", err, "event_type", eventType)
	}
}

// GetRecent retrieves recent audit entries.
func (a *AuditLogger) GetRecent(ctx context.Context, limit int) ([]database.AuditLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT id, timestamp, event_type, request_id, api_key_id, actor, details, ip_address
		FROM audit_log
		ORDER BY timestamp DESC
		LIMIT ?
	`, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []database.AuditLogEntry
	for rows.Next() {
		var (
			entry       database.AuditLogEntry
			timestamp   string
			detailsJSON []byte
		)

		if err := rows.Scan(
			&entry.ID, &timestamp, &entry.EventType,
			&entry.RequestID, &entry.APIKeyID, &entry.Actor,
			&detailsJSON, &entry.IPAddress,
		); err != nil {
			return nil, err
		}

		entry.Timestamp, _ = util.ParseSQLiteTimestamp(timestamp)
		if len(detailsJSON) > 0 {
			entry.Details = detailsJSON
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// GetByRequestID retrieves audit entries for a specific request.
func (a *AuditLogger) GetByRequestID(ctx context.Context, requestID string) ([]database.AuditLogEntry, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, timestamp, event_type, request_id, api_key_id, actor, details, ip_address
		FROM audit_log
		WHERE request_id = ?
		ORDER BY timestamp ASC
	`, requestID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []database.AuditLogEntry
	for rows.Next() {
		var (
			entry       database.AuditLogEntry
			timestamp   string
			detailsJSON []byte
		)

		if err := rows.Scan(
			&entry.ID, &timestamp, &entry.EventType,
			&entry.RequestID, &entry.APIKeyID, &entry.Actor,
			&detailsJSON, &entry.IPAddress,
		); err != nil {
			return nil, err
		}

		entry.Timestamp, _ = util.ParseSQLiteTimestamp(timestamp)
		if len(detailsJSON) > 0 {
			entry.Details = detailsJSON
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// GetByEventType retrieves audit entries of a specific type.
func (a *AuditLogger) GetByEventType(ctx context.Context, eventType string, limit int) ([]database.AuditLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT id, timestamp, event_type, request_id, api_key_id, actor, details, ip_address
		FROM audit_log
		WHERE event_type = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, eventType, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []database.AuditLogEntry
	for rows.Next() {
		var (
			entry       database.AuditLogEntry
			timestamp   string
			detailsJSON []byte
		)

		if err := rows.Scan(
			&entry.ID, &timestamp, &entry.EventType,
			&entry.RequestID, &entry.APIKeyID, &entry.Actor,
			&detailsJSON, &entry.IPAddress,
		); err != nil {
			return nil, err
		}

		entry.Timestamp, _ = util.ParseSQLiteTimestamp(timestamp)
		if len(detailsJSON) > 0 {
			entry.Details = detailsJSON
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// Count returns the total number of audit entries.
func (a *AuditLogger) Count(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&count)
	return count, err
}

// DeleteOlderThan removes audit entries older than the specified number of days.
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
