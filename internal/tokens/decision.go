// Package tokens provides single-use decision tokens for approval callbacks.
package tokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Errors returned when a token cannot be used. They are safe to show to the
// person clicking an approval link.
var (
	ErrTokenNotFound = errors.New("this approval link is not valid")
	ErrTokenConsumed = errors.New("this approval link has already been used")
	ErrTokenExpired  = errors.New("this approval link has expired")
	ErrActionAllowed = errors.New("this approval link does not permit that action")
)

// Actions a decision token may authorize.
const (
	ActionApprove = "approve"
	ActionDeny    = "deny"
	ActionSuggest = "suggest"
)

// Repository handles decision token storage and validation.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new decision token repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// Create generates and stores a decision token for a request, returning the
// token itself. Only its hash is persisted, so a database disclosure does not
// hand an attacker usable approval links.
func (r *Repository) Create(ctx context.Context, requestID string, expiresAt time.Time) (string, error) {
	token, hash, err := crypto.GenerateDecisionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	allowedActions, err := json.Marshal([]string{ActionApprove, ActionDeny, ActionSuggest})
	if err != nil {
		return "", fmt.Errorf("failed to encode allowed actions: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO decision_tokens (token_hash, request_id, allowed_actions, expires_at)
		VALUES (?, ?, ?, ?)
	`, hash, requestID, string(allowedActions), util.SQLiteTimestamp(expiresAt)); err != nil {
		return "", fmt.Errorf("failed to store token: %w", err)
	}

	return token, nil
}

// ValidateResult describes a token that has been looked up but not consumed.
type ValidateResult struct {
	RequestID      string
	AllowedActions []string
}

// Validate checks that a token exists, is unconsumed, and has not expired,
// without consuming it. The returned error is one of the sentinel errors above.
func (r *Repository) Validate(ctx context.Context, token string) (*ValidateResult, error) {
	hash := crypto.HashSHA256(token)

	var (
		requestID   string
		allowedJSON string
		expiresAt   string
		consumedAt  sql.NullString
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT request_id, allowed_actions, expires_at, consumed_at
		FROM decision_tokens
		WHERE token_hash = ?
	`, hash).Scan(&requestID, &allowedJSON, &expiresAt, &consumedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	if consumedAt.Valid {
		return nil, ErrTokenConsumed
	}

	expires, err := util.ParseSQLiteTimestamp(expiresAt)
	if err != nil || !time.Now().Before(expires) {
		return nil, ErrTokenExpired
	}

	var allowedActions []string
	if err := json.Unmarshal([]byte(allowedJSON), &allowedActions); err != nil {
		return nil, fmt.Errorf("corrupt allowed_actions for token: %w", err)
	}

	return &ValidateResult{RequestID: requestID, AllowedActions: allowedActions}, nil
}

// Consume validates a token and marks it used in one atomic step, returning the
// request it authorizes. Concurrent callers race on the conditional UPDATE, so
// exactly one of them can act on a given token.
func (r *Repository) Consume(ctx context.Context, token, action string) (string, error) {
	result, err := r.Validate(ctx, token)
	if err != nil {
		return "", err
	}

	if !slices.Contains(result.AllowedActions, action) {
		return "", ErrActionAllowed
	}

	sqlResult, err := r.db.ExecContext(ctx, `
		UPDATE decision_tokens
		SET consumed_at = datetime('now'), consumed_action = ?
		WHERE token_hash = ? AND consumed_at IS NULL
	`, action, crypto.HashSHA256(token))
	if err != nil {
		return "", fmt.Errorf("failed to consume token: %w", err)
	}

	rowsAffected, err := sqlResult.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("failed to consume token: %w", err)
	}
	if rowsAffected == 0 {
		// Another request consumed the token between Validate and here.
		return "", ErrTokenConsumed
	}

	return result.RequestID, nil
}

// DeleteExpired removes tokens that can no longer be used.
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
