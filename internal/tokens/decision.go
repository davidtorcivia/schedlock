// Package tokens provides decision token management for approval callbacks.
package tokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
)

// Repository handles decision token storage and validation.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new decision token repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// Create generates and stores a new decision token for a request.
// Returns the token (to be used in URLs) - store the hash only.
func (r *Repository) Create(ctx context.Context, requestID string, expiresAt time.Time) (string, error) {
	token, hash, err := crypto.GenerateDecisionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	allowedActions, _ := json.Marshal([]string{"approve", "deny", "suggest"})

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO decision_tokens (token_hash, request_id, allowed_actions, expires_at)
		VALUES (?, ?, ?, ?)
	`, hash, requestID, string(allowedActions), expiresAt.Format(time.RFC3339))

	if err != nil {
		return "", fmt.Errorf("failed to store token: %w", err)
	}

	return token, nil
}

// ValidateResult contains the result of token validation.
type ValidateResult struct {
	RequestID      string
	AllowedActions []string
	Valid          bool
	Error          string
}

// Validate checks if a token is valid without consuming it.
func (r *Repository) Validate(ctx context.Context, token string) (*ValidateResult, error) {
	hash := crypto.HashSHA256(token)

	var (
		requestID      string
		allowedJSON    string
		expiresAt      string
		consumedAt     sql.NullString
		consumedAction sql.NullString
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT request_id, allowed_actions, expires_at, consumed_at, consumed_action
		FROM decision_tokens
		WHERE token_hash = ?
	`, hash).Scan(&requestID, &allowedJSON, &expiresAt, &consumedAt, &consumedAction)

	if err == sql.ErrNoRows {
		return &ValidateResult{Valid: false, Error: "token not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check if already consumed
	if consumedAt.Valid {
		return &ValidateResult{
			RequestID: requestID,
			Valid:     false,
			Error:     fmt.Sprintf("token already used for action: %s", consumedAction.String),
		}, nil
	}

	// Check if expired
	expires, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(expires) {
		return &ValidateResult{
			RequestID: requestID,
			Valid:     false,
			Error:     "token expired",
		}, nil
	}

	// Parse allowed actions
	var allowedActions []string
	json.Unmarshal([]byte(allowedJSON), &allowedActions)

	return &ValidateResult{
		RequestID:      requestID,
		AllowedActions: allowedActions,
		Valid:          true,
	}, nil
}

// Consume validates and consumes a token in a single atomic operation.
// Returns the request ID if successful.
func (r *Repository) Consume(ctx context.Context, token, action string) (string, error) {
	// First validate
	result, err := r.Validate(ctx, token)
	if err != nil {
		return "", err
	}

	if !result.Valid {
		return "", fmt.Errorf(result.Error)
	}

	// Check if action is allowed
	actionAllowed := false
	for _, a := range result.AllowedActions {
		if a == action {
			actionAllowed = true
			break
		}
	}
	if !actionAllowed {
		return "", fmt.Errorf("action %q not allowed for this token", action)
	}

	// Atomically consume the token
	hash := crypto.HashSHA256(token)
	sqlResult, err := r.db.ExecContext(ctx, `
		UPDATE decision_tokens
		SET consumed_at = datetime('now'), consumed_action = ?
		WHERE token_hash = ? AND consumed_at IS NULL
	`, action, hash)

	if err != nil {
		return "", fmt.Errorf("failed to consume token: %w", err)
	}

	rowsAffected, _ := sqlResult.RowsAffected()
	if rowsAffected == 0 {
		// Token was consumed by another request (race condition)
		return "", fmt.Errorf("token already consumed")
	}

	return result.RequestID, nil
}

// GetByRequestID retrieves all tokens for a request.
func (r *Repository) GetByRequestID(ctx context.Context, requestID string) ([]database.DecisionToken, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT token_hash, request_id, allowed_actions, expires_at, consumed_at, consumed_action, created_at
		FROM decision_tokens
		WHERE request_id = ?
	`, requestID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []database.DecisionToken
	for rows.Next() {
		var (
			tok         database.DecisionToken
			allowedJSON string
			expiresAt   string
			consumedAt  sql.NullString
			createdAt   string
		)

		if err := rows.Scan(
			&tok.TokenHash, &tok.RequestID, &allowedJSON,
			&expiresAt, &consumedAt, &tok.ConsumedAction, &createdAt,
		); err != nil {
			return nil, err
		}

		json.Unmarshal([]byte(allowedJSON), &tok.AllowedActions)
		tok.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		tok.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)

		if consumedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", consumedAt.String)
			tok.ConsumedAt = sql.NullTime{Time: t, Valid: true}
		}

		tokens = append(tokens, tok)
	}

	return tokens, rows.Err()
}

// DeleteExpired removes all expired tokens.
func (r *Repository) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM decision_tokens
		WHERE expires_at < datetime('now')
	`)

	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// DeleteByRequestID removes all tokens for a request.
func (r *Repository) DeleteByRequestID(ctx context.Context, requestID string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM decision_tokens
		WHERE request_id = ?
	`, requestID)

	return err
}
