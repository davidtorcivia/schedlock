// Package requests provides request storage and management.
package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Repository handles request storage and retrieval.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new request repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// CreateRequest contains the data needed to create a new request.
type CreateRequest struct {
	APIKeyID    string
	Operation   string
	Payload     json.RawMessage
	ExpiresAt   time.Time
}

// Create stores a new request.
func (r *Repository) Create(ctx context.Context, req *CreateRequest) (*database.Request, error) {
	id, err := crypto.GenerateRequestID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate request ID: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, req.APIKeyID, req.Operation, database.StatusPendingApproval, string(req.Payload), util.SQLiteTimestamp(req.ExpiresAt))

	if err != nil {
		return nil, fmt.Errorf("failed to insert request: %w", err)
	}

	return r.GetByID(ctx, id)
}

// GetByID retrieves a request by its ID.
func (r *Repository) GetByID(ctx context.Context, id string) (*database.Request, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, api_key_id, operation, status, payload, result, error,
		       suggestion_text, suggestion_at, suggestion_by,
		       created_at, expires_at, decided_at, decided_by,
		       executed_at, retry_count, webhook_notified_at
		FROM requests
		WHERE id = ?
	`, id)

	return scanRequest(row)
}

// GetByAPIKeyID retrieves all requests for an API key.
func (r *Repository) GetByAPIKeyID(ctx context.Context, apiKeyID string, limit int) ([]database.Request, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, api_key_id, operation, status, payload, result, error,
		       suggestion_text, suggestion_at, suggestion_by,
		       created_at, expires_at, decided_at, decided_by,
		       executed_at, retry_count, webhook_notified_at
		FROM requests
		WHERE api_key_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, apiKeyID, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// GetPending retrieves all pending requests.
func (r *Repository) GetPending(ctx context.Context) ([]database.Request, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, api_key_id, operation, status, payload, result, error,
		       suggestion_text, suggestion_at, suggestion_by,
		       created_at, expires_at, decided_at, decided_by,
		       executed_at, retry_count, webhook_notified_at
		FROM requests
		WHERE status = ?
		ORDER BY created_at ASC
	`, database.StatusPendingApproval)

	if err != nil {
		return nil, fmt.Errorf("failed to query pending requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// GetExpired retrieves all expired pending requests.
func (r *Repository) GetExpired(ctx context.Context) ([]database.Request, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, api_key_id, operation, status, payload, result, error,
		       suggestion_text, suggestion_at, suggestion_by,
		       created_at, expires_at, decided_at, decided_by,
		       executed_at, retry_count, webhook_notified_at
		FROM requests
		WHERE status = ? AND expires_at < datetime('now')
	`, database.StatusPendingApproval)

	if err != nil {
		return nil, fmt.Errorf("failed to query expired requests: %w", err)
	}
	defer rows.Close()

	return scanRequests(rows)
}

// UpdateStatus atomically updates a request's status.
// Returns true if the update succeeded, false if the request was already transitioned.
func (r *Repository) UpdateStatus(ctx context.Context, id, newStatus, decidedBy string) (bool, error) {
	// Only allow transition from pending_approval
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, decided_at = datetime('now'), decided_by = ?
		WHERE id = ? AND status = ?
	`, newStatus, decidedBy, id, database.StatusPendingApproval)

	if err != nil {
		return false, fmt.Errorf("failed to update status: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected > 0, nil
}

// UpdateStatusFrom atomically updates status from a specific status.
func (r *Repository) UpdateStatusFrom(ctx context.Context, id, fromStatus, toStatus string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?
		WHERE id = ? AND status = ?
	`, toStatus, id, fromStatus)

	if err != nil {
		return false, fmt.Errorf("failed to update status: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected > 0, nil
}

// SetSuggestion stores a change suggestion for a request.
func (r *Repository) SetSuggestion(ctx context.Context, id, text, by string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, suggestion_text = ?, suggestion_at = datetime('now'), suggestion_by = ?
		WHERE id = ? AND status = ?
	`, database.StatusChangeRequested, text, by, id, database.StatusPendingApproval)

	if err != nil {
		return fmt.Errorf("failed to set suggestion: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("request not found or already resolved")
	}

	return nil
}

// UpdatePayload updates the payload for a pending request.
func (r *Repository) UpdatePayload(ctx context.Context, id string, payload json.RawMessage) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET payload = ?
		WHERE id = ? AND status = ?
	`, payload, id, database.StatusPendingApproval)

	if err != nil {
		return fmt.Errorf("failed to update payload: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("request not found or not pending")
	}

	return nil
}

// SetResult stores the execution result.
func (r *Repository) SetResult(ctx context.Context, id string, result json.RawMessage) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, result = ?, executed_at = datetime('now')
		WHERE id = ?
	`, database.StatusCompleted, string(result), id)

	return err
}

// SetError stores the execution error.
func (r *Repository) SetError(ctx context.Context, id, errorMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, error = ?
		WHERE id = ?
	`, database.StatusFailed, errorMsg, id)

	return err
}

// SetExecuting marks a request as currently executing.
func (r *Repository) SetExecuting(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?
		WHERE id = ? AND status = ?
	`, database.StatusExecuting, id, database.StatusApproved)

	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("request not approved or already executing")
	}

	return nil
}

// IncrementRetryCount increments the retry counter.
func (r *Repository) IncrementRetryCount(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET retry_count = retry_count + 1, status = ?
		WHERE id = ?
	`, database.StatusApproved, id)

	return err
}

// SetWebhookNotified marks the webhook as sent.
func (r *Repository) SetWebhookNotified(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET webhook_notified_at = datetime('now')
		WHERE id = ?
	`, id)

	return err
}

// Cancel marks a request as cancelled.
func (r *Repository) Cancel(ctx context.Context, id, apiKeyID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE requests
		SET status = ?, decided_at = datetime('now'), decided_by = 'api'
		WHERE id = ? AND api_key_id = ? AND status = ?
	`, database.StatusCancelled, id, apiKeyID, database.StatusPendingApproval)

	if err != nil {
		return fmt.Errorf("failed to cancel request: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("request not found, not owned by this key, or already resolved")
	}

	return nil
}

// FindByIdempotencyKey finds a request by its idempotency key.
func (r *Repository) FindByIdempotencyKey(ctx context.Context, apiKeyID, key string) (*database.Request, error) {
	var requestID string
	err := r.db.QueryRowContext(ctx, `
		SELECT request_id FROM idempotency_keys
		WHERE api_key_id = ? AND idempotency_key = ?
		AND created_at > datetime('now', '-24 hours')
	`, apiKeyID, key).Scan(&requestID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	return r.GetByID(ctx, requestID)
}

// StoreIdempotencyKey stores an idempotency key mapping.
func (r *Repository) StoreIdempotencyKey(ctx context.Context, apiKeyID, key, requestID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys (api_key_id, idempotency_key, request_id)
		VALUES (?, ?, ?)
	`, apiKeyID, key, requestID)

	return err
}

// GetStats returns request statistics.
func (r *Repository) GetStats(ctx context.Context) (*RequestStats, error) {
	stats := &RequestStats{}

	// Count by status
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM requests
		WHERE created_at > datetime('now', '-1 day')
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats.StatusCounts = make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats.StatusCounts[status] = count
	}

	// Total pending
	r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM requests WHERE status = ?
	`, database.StatusPendingApproval).Scan(&stats.TotalPending)

	// Total today
	r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM requests WHERE created_at > datetime('now', '-1 day')
	`).Scan(&stats.TotalToday)

	return stats, nil
}

// RequestStats contains aggregate statistics.
type RequestStats struct {
	StatusCounts map[string]int
	TotalPending int
	TotalToday   int
}

// Helper functions

func scanRequest(row *sql.Row) (*database.Request, error) {
	var (
		req                database.Request
		payload            string
		result             sql.NullString
		createdAt          string
		expiresAt          string
		suggestionAt       sql.NullString
		decidedAt          sql.NullString
		executedAt         sql.NullString
		webhookNotifiedAt  sql.NullString
	)

	err := row.Scan(
		&req.ID, &req.APIKeyID, &req.Operation, &req.Status,
		&payload, &result, &req.Error,
		&req.SuggestionText, &suggestionAt, &req.SuggestionBy,
		&createdAt, &expiresAt, &decidedAt, &req.DecidedBy,
		&executedAt, &req.RetryCount, &webhookNotifiedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan request: %w", err)
	}

	req.Payload = json.RawMessage(payload)
	if result.Valid {
		req.Result = json.RawMessage(result.String)
	}

	req.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	req.ExpiresAt, _ = time.Parse("2006-01-02 15:04:05", expiresAt)

	if suggestionAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", suggestionAt.String)
		req.SuggestionAt = sql.NullTime{Time: t, Valid: true}
	}
	if decidedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", decidedAt.String)
		req.DecidedAt = sql.NullTime{Time: t, Valid: true}
	}
	if executedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", executedAt.String)
		req.ExecutedAt = sql.NullTime{Time: t, Valid: true}
	}
	if webhookNotifiedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", webhookNotifiedAt.String)
		req.WebhookNotifiedAt = sql.NullTime{Time: t, Valid: true}
	}

	return &req, nil
}

func scanRequests(rows *sql.Rows) ([]database.Request, error) {
	var requests []database.Request

	for rows.Next() {
		var (
			req                database.Request
			payload            string
			result             sql.NullString
			createdAt          string
			expiresAt          string
			suggestionAt       sql.NullString
			decidedAt          sql.NullString
			executedAt         sql.NullString
			webhookNotifiedAt  sql.NullString
		)

		err := rows.Scan(
			&req.ID, &req.APIKeyID, &req.Operation, &req.Status,
			&payload, &result, &req.Error,
			&req.SuggestionText, &suggestionAt, &req.SuggestionBy,
			&createdAt, &expiresAt, &decidedAt, &req.DecidedBy,
			&executedAt, &req.RetryCount, &webhookNotifiedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan request: %w", err)
		}

		req.Payload = json.RawMessage(payload)
		if result.Valid {
			req.Result = json.RawMessage(result.String)
		}

		req.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		req.ExpiresAt, _ = time.Parse("2006-01-02 15:04:05", expiresAt)

		if suggestionAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", suggestionAt.String)
			req.SuggestionAt = sql.NullTime{Time: t, Valid: true}
		}
		if decidedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", decidedAt.String)
			req.DecidedAt = sql.NullTime{Time: t, Valid: true}
		}
		if executedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", executedAt.String)
			req.ExecutedAt = sql.NullTime{Time: t, Valid: true}
		}
		if webhookNotifiedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", webhookNotifiedAt.String)
			req.WebhookNotifiedAt = sql.NullTime{Time: t, Valid: true}
		}

		requests = append(requests, req)
	}

	return requests, rows.Err()
}
