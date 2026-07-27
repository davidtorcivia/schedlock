// Package apikeys provides API key issuance, authentication, and lifecycle
// management.
package apikeys

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

// Authentication failures. Callers must not distinguish these to the client:
// telling an attacker whether a key exists, is revoked, or has merely expired
// is more information than they need.
var (
	ErrKeyNotFound = errors.New("API key not found")
	ErrKeyRevoked  = errors.New("API key has been revoked")
	ErrKeyExpired  = errors.New("API key has expired")
	ErrKeyMalfored = errors.New("API key is malformed")
)

// ErrKeyNotRevocable is returned when a key cannot be revoked.
var ErrKeyNotRevocable = errors.New("API key not found or already revoked")

// keyColumns is the shared SELECT list for full key records.
const keyColumns = `
	id, key_hash, key_prefix, name, tier, constraints,
	created_at, last_used_at, expires_at, revoked_at, rate_limit_override`

// Repository handles API key storage and retrieval.
type Repository struct {
	db     *database.DB
	hasher *crypto.APIKeyHasher
}

// NewRepository creates a new API key repository.
func NewRepository(db *database.DB, hasher *crypto.APIKeyHasher) *Repository {
	return &Repository{db: db, hasher: hasher}
}

// AuthenticatedKey is the subset of a key's record needed to authorize a
// request.
type AuthenticatedKey struct {
	ID          string
	KeyPrefix   string
	Name        string
	Tier        string
	Constraints *database.KeyConstraints
}

// Create issues a new API key, returning the stored record and the full key.
// The full key is the only copy that will ever exist in plaintext.
func (r *Repository) Create(ctx context.Context, name, tier string, constraints *database.KeyConstraints) (*database.APIKey, string, error) {
	fullKey, err := r.hasher.GenerateAPIKey(tier)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate API key: %w", err)
	}

	keyID, err := crypto.GenerateAPIKeyID()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate key ID: %w", err)
	}

	var constraintsJSON []byte
	if constraints != nil {
		if constraintsJSON, err = json.Marshal(constraints); err != nil {
			return nil, "", fmt.Errorf("failed to serialize constraints: %w", err)
		}
	}

	keyHash := r.hasher.HashAPIKey(fullKey)
	keyPrefix := crypto.GetKeyPrefix(fullKey)

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, key_hash, key_prefix, name, tier, constraints, created_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
	`, keyID, keyHash, keyPrefix, name, tier, nullableJSON(constraintsJSON)); err != nil {
		return nil, "", fmt.Errorf("failed to insert API key: %w", err)
	}

	return &database.APIKey{
		ID:          keyID,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Name:        name,
		Tier:        tier,
		Constraints: constraints,
		CreatedAt:   time.Now().UTC(),
	}, fullKey, nil
}

// Authenticate validates a presented API key and returns its authorization
// context.
func (r *Repository) Authenticate(ctx context.Context, key string) (*AuthenticatedKey, error) {
	if crypto.ParseAPIKeyTier(key) == "" {
		return nil, ErrKeyMalfored
	}

	var (
		id              string
		keyPrefix       string
		name            string
		storedTier      string
		constraintsJSON sql.NullString
		expiresAt       sql.NullString
		revokedAt       sql.NullString
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id, key_prefix, name, tier, constraints, expires_at, revoked_at
		FROM api_keys
		WHERE key_hash = ?
	`, r.hasher.HashAPIKey(key)).Scan(&id, &keyPrefix, &name, &storedTier, &constraintsJSON, &expiresAt, &revokedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	if revoked := database.NullTimeText(revokedAt); revoked.Valid {
		return nil, ErrKeyRevoked
	}
	if expires := database.NullTimeText(expiresAt); expires.Valid && !time.Now().Before(expires.Time) {
		return nil, ErrKeyExpired
	}

	constraints, err := decodeConstraints(constraintsJSON)
	if err != nil {
		return nil, err
	}

	return &AuthenticatedKey{
		ID:          id,
		KeyPrefix:   keyPrefix,
		Name:        name,
		Tier:        storedTier,
		Constraints: constraints,
	}, nil
}

// GetByID retrieves a key record, returning (nil, nil) when absent.
func (r *Repository) GetByID(ctx context.Context, id string) (*database.APIKey, error) {
	row := r.db.QueryRowContext(ctx, `SELECT`+keyColumns+` FROM api_keys WHERE id = ?`, id)

	key, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return key, nil
}

// List returns stored keys, newest first.
func (r *Repository) List(ctx context.Context, includeRevoked bool) ([]database.APIKey, error) {
	query := `SELECT` + keyColumns + ` FROM api_keys`
	if !includeRevoked {
		query += ` WHERE revoked_at IS NULL`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}
	defer rows.Close()

	keys := make([]database.APIKey, 0)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		keys = append(keys, *key)
	}

	return keys, rows.Err()
}

// Revoke permanently disables a key.
func (r *Repository) Revoke(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_keys
		SET revoked_at = datetime('now')
		WHERE id = ? AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	if affected == 0 {
		return ErrKeyNotRevocable
	}
	return nil
}

// UpdateLastUsed records that a key was used. Callers should prefer the
// UsageRecorder, which coalesces these writes off the request path.
func (r *Repository) UpdateLastUsed(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	stmt, err := tx.PrepareContext(ctx, `UPDATE api_keys SET last_used_at = datetime('now') WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UpdateConstraints replaces the policy constraints attached to a key.
func (r *Repository) UpdateConstraints(ctx context.Context, id string, constraints *database.KeyConstraints) error {
	var constraintsJSON []byte
	if constraints != nil {
		var err error
		if constraintsJSON, err = json.Marshal(constraints); err != nil {
			return fmt.Errorf("failed to serialize constraints: %w", err)
		}
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE api_keys SET constraints = ? WHERE id = ?
	`, nullableJSON(constraintsJSON), id)
	return err
}

// Count returns the number of active keys per tier.
func (r *Repository) Count(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT tier, COUNT(*)
		FROM api_keys
		WHERE revoked_at IS NULL
		GROUP BY tier
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var tier string
		var count int
		if err := rows.Scan(&tier, &count); err != nil {
			return nil, err
		}
		counts[tier] = count
	}

	return counts, rows.Err()
}

// scanAPIKey reads one key row. The argument order must match keyColumns.
func scanAPIKey(s database.Scanner) (*database.APIKey, error) {
	var (
		key             database.APIKey
		constraintsJSON sql.NullString
		createdAt       sql.NullString
		lastUsedAt      sql.NullString
		expiresAt       sql.NullString
		revokedAt       sql.NullString
	)

	if err := s.Scan(
		&key.ID, &key.KeyHash, &key.KeyPrefix, &key.Name, &key.Tier, &constraintsJSON,
		&createdAt, &lastUsedAt, &expiresAt, &revokedAt, &key.RateLimitOverride,
	); err != nil {
		return nil, err
	}

	constraints, err := decodeConstraints(constraintsJSON)
	if err != nil {
		// A single corrupt constraints blob must not make the key list
		// unreadable; the key is surfaced with no constraints and the problem
		// is logged.
		util.Warn("Ignoring unreadable key constraints", "key_id", key.ID, "error", err)
	}
	key.Constraints = constraints

	if created := database.NullTimeText(createdAt); created.Valid {
		key.CreatedAt = created.Time
	}
	key.LastUsedAt = database.NullTimeText(lastUsedAt)
	key.ExpiresAt = database.NullTimeText(expiresAt)
	key.RevokedAt = database.NullTimeText(revokedAt)

	return &key, nil
}

func decodeConstraints(raw sql.NullString) (*database.KeyConstraints, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	constraints := &database.KeyConstraints{}
	if err := json.Unmarshal([]byte(raw.String), constraints); err != nil {
		return nil, fmt.Errorf("failed to parse constraints: %w", err)
	}
	return constraints, nil
}

func nullableJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}
