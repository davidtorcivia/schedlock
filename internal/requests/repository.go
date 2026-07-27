// Package requests provides storage and lifecycle management for calendar
// operation requests.
package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// ErrNotPending is returned when an operation requires a request that is still
// awaiting a decision.
var ErrNotPending = errors.New("request not found or no longer pending")

// requestColumns is the single source of truth for the SELECT list, so the
// column order can never drift from scanRequest's argument order.
const requestColumns = `
	id, api_key_id, operation, status, payload, result, error,
	suggestion_text, suggestion_at, suggestion_by,
	created_at, expires_at, decided_at, decided_by,
	executed_at, retry_count, webhook_notified_at`

// Repository handles request storage and retrieval.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new request repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// CreateRequest describes a request to be stored.
type CreateRequest struct {
	APIKeyID  string
	Operation string
	Payload   json.RawMessage
	ExpiresAt time.Time
}

// Create stores a new request in the pending state.
func (r *Repository) Create(ctx context.Context, req *CreateRequest) (*database.Request, error) {
	id, err := crypto.GenerateRequestID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate request ID: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, req.APIKeyID, req.Operation, database.StatusPendingApproval,
		string(req.Payload), util.SQLiteTimestamp(req.ExpiresAt)); err != nil {
		return nil, fmt.Errorf("failed to insert request: %w", err)
	}

	return r.GetByID(ctx, id)
}

// GetByID retrieves a request, returning (nil, nil) when it does not exist.
func (r *Repository) GetByID(ctx context.Context, id string) (*database.Request, error) {
	row := r.db.QueryRowContext(ctx, `SELECT`+requestColumns+` FROM requests WHERE id = ?`, id)

	req, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return req, nil
}

// GetByAPIKeyID retrieves the most recent requests issued by an API key.
func (r *Repository) GetByAPIKeyID(ctx context.Context, apiKeyID string, limit int) ([]database.Request, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.QueryContext(ctx, `SELECT`+requestColumns+`
		FROM requests
		WHERE api_key_id = ?
		ORDER BY created_at DESC
		LIMIT ?`, apiKeyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// GetPending retrieves every request still awaiting a decision.
func (r *Repository) GetPending(ctx context.Context) ([]database.Request, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT`+requestColumns+`
		FROM requests
		WHERE status = ?
		ORDER BY created_at ASC`, database.StatusPendingApproval)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// GetExpired retrieves pending requests whose approval window has elapsed.
func (r *Repository) GetExpired(ctx context.Context) ([]database.Request, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT`+requestColumns+`
		FROM requests
		WHERE status = ? AND expires_at < datetime('now')`, database.StatusPendingApproval)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// UpdateStatus records a decision, transitioning out of pending_approval.
//
// The status guard in the WHERE clause is what makes a decision single-shot:
// two approvers acting at once, or a callback racing the timeout worker, will
// see exactly one update succeed.
func (r *Repository) UpdateStatus(ctx context.Context, id, newStatus, decidedBy string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, decided_at = datetime('now'), decided_by = ?
		WHERE id = ? AND status = ?
	`, newStatus, decidedBy, id, database.StatusPendingApproval)
	if err != nil {
		return false, fmt.Errorf("failed to update status: %w", err)
	}

	return rowsChanged(result)
}

// SetSuggestion records requested changes and moves the request out of the
// pending state.
func (r *Repository) SetSuggestion(ctx context.Context, id, text, by string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, suggestion_text = ?, suggestion_at = datetime('now'), suggestion_by = ?
		WHERE id = ? AND status = ?
	`, database.StatusChangeRequested, text, by, id, database.StatusPendingApproval)
	if err != nil {
		return fmt.Errorf("failed to set suggestion: %w", err)
	}

	changed, err := rowsChanged(result)
	if err != nil {
		return err
	}
	if !changed {
		return ErrNotPending
	}
	return nil
}

// UpdatePayload replaces the payload of a request that is still pending.
func (r *Repository) UpdatePayload(ctx context.Context, id string, payload json.RawMessage) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET payload = ?
		WHERE id = ? AND status = ?
	`, string(payload), id, database.StatusPendingApproval)
	if err != nil {
		return fmt.Errorf("failed to update payload: %w", err)
	}

	changed, err := rowsChanged(result)
	if err != nil {
		return err
	}
	if !changed {
		return ErrNotPending
	}
	return nil
}

// SetExecuting claims an approved request for execution. Only one worker can
// win the transition, so a re-queued request is never executed twice.
func (r *Repository) SetExecuting(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?
		WHERE id = ? AND status = ?
	`, database.StatusExecuting, id, database.StatusApproved)
	if err != nil {
		return false, fmt.Errorf("failed to mark request executing: %w", err)
	}

	return rowsChanged(result)
}

// SetResult stores a successful execution result.
func (r *Repository) SetResult(ctx context.Context, id string, result json.RawMessage) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, result = ?, executed_at = datetime('now'), error = NULL
		WHERE id = ?
	`, database.StatusCompleted, string(result), id)
	return err
}

// SetError records a terminal execution failure.
func (r *Repository) SetError(ctx context.Context, id, errorMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, error = ?, executed_at = datetime('now')
		WHERE id = ?
	`, database.StatusFailed, errorMsg, id)
	return err
}

// ScheduleRetry increments the attempt counter and returns the request to the
// approved state so a worker may claim it again.
func (r *Repository) ScheduleRetry(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET retry_count = retry_count + 1, status = ?
		WHERE id = ? AND status = ?
	`, database.StatusApproved, id, database.StatusExecuting)
	return err
}

// SetWebhookNotified records that the client webhook has been delivered.
func (r *Repository) SetWebhookNotified(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET webhook_notified_at = datetime('now')
		WHERE id = ?
	`, id)
	return err
}

// Cancel withdraws a pending request on behalf of the API key that created it.
func (r *Repository) Cancel(ctx context.Context, id, apiKeyID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, decided_at = datetime('now'), decided_by = 'api'
		WHERE id = ? AND api_key_id = ? AND status = ?
	`, database.StatusCancelled, id, apiKeyID, database.StatusPendingApproval)
	if err != nil {
		return fmt.Errorf("failed to cancel request: %w", err)
	}

	changed, err := rowsChanged(result)
	if err != nil {
		return err
	}
	if !changed {
		return ErrNotPending
	}
	return nil
}

// FindByIdempotencyKey returns the request previously created under an
// idempotency key, or (nil, nil) if there is none within the retention window.
func (r *Repository) FindByIdempotencyKey(ctx context.Context, apiKeyID, key string) (*database.Request, error) {
	var requestID string
	err := r.db.QueryRowContext(ctx, `
		SELECT request_id FROM idempotency_keys
		WHERE api_key_id = ? AND idempotency_key = ?
		AND created_at > datetime('now', '-24 hours')
	`, apiKeyID, key).Scan(&requestID)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	return r.GetByID(ctx, requestID)
}

// ClaimIdempotencyKey records the mapping from an idempotency key to a request.
//
// It reports whether this caller won the claim. Two concurrent submissions with
// the same key both pass the earlier lookup, so the primary key conflict here
// is the actual arbiter of which request the client is told about.
func (r *Repository) ClaimIdempotencyKey(ctx context.Context, apiKeyID, key, requestID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys (api_key_id, idempotency_key, request_id)
		VALUES (?, ?, ?)
		ON CONFLICT (api_key_id, idempotency_key) DO NOTHING
	`, apiKeyID, key, requestID)
	if err != nil {
		return false, err
	}

	return rowsChanged(result)
}

// Delete removes a request. Dependent rows cascade; audit entries survive with
// a NULL request reference.
func (r *Repository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM requests WHERE id = ?`, id)
	return err
}

// RequestStats contains aggregate request counts for the dashboard.
type RequestStats struct {
	StatusCounts map[string]int
	TotalPending int
	TotalLastDay int
}

// GetStats returns request statistics for the last 24 hours.
func (r *Repository) GetStats(ctx context.Context) (*RequestStats, error) {
	stats := &RequestStats{StatusCounts: make(map[string]int)}

	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM requests
		WHERE created_at > datetime('now', '-1 day')
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats.StatusCounts[status] = count
		stats.TotalLastDay += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM requests WHERE status = ?
	`, database.StatusPendingApproval).Scan(&stats.TotalPending); err != nil {
		return nil, err
	}

	return stats, nil
}

func rowsChanged(result interface{ RowsAffected() (int64, error) }) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read affected rows: %w", err)
	}
	return affected > 0, nil
}

// scanRequest reads one request row. The argument order must match
// requestColumns.
func scanRequest(s database.Scanner) (*database.Request, error) {
	var (
		req               database.Request
		payload           string
		result            sql.NullString
		createdAt         string
		expiresAt         string
		suggestionAt      sql.NullString
		decidedAt         sql.NullString
		executedAt        sql.NullString
		webhookNotifiedAt sql.NullString
	)

	if err := s.Scan(
		&req.ID, &req.APIKeyID, &req.Operation, &req.Status,
		&payload, &result, &req.Error,
		&req.SuggestionText, &suggestionAt, &req.SuggestionBy,
		&createdAt, &expiresAt, &decidedAt, &req.DecidedBy,
		&executedAt, &req.RetryCount, &webhookNotifiedAt,
	); err != nil {
		return nil, err
	}

	req.Payload = json.RawMessage(payload)
	if result.Valid {
		req.Result = json.RawMessage(result.String)
	}

	req.CreatedAt = database.TimeText(createdAt)
	req.ExpiresAt = database.TimeText(expiresAt)
	req.SuggestionAt = database.NullTimeText(suggestionAt)
	req.DecidedAt = database.NullTimeText(decidedAt)
	req.ExecutedAt = database.NullTimeText(executedAt)
	req.WebhookNotifiedAt = database.NullTimeText(webhookNotifiedAt)

	return &req, nil
}

func scanRequests(rows *sql.Rows) ([]database.Request, error) {
	requests := make([]database.Request, 0)

	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan request: %w", err)
		}
		requests = append(requests, *req)
	}

	return requests, rows.Err()
}
