// Package apikeys provides API key management functionality.
package apikeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
)

// Repository handles API key storage and retrieval.
type Repository struct {
	db     *database.DB
	hasher *crypto.APIKeyHasher
}

// NewRepository creates a new API key repository.
func NewRepository(db *database.DB, hasher *crypto.APIKeyHasher) *Repository {
	return &Repository{
		db:     db,
		hasher: hasher,
	}
}

// AuthenticatedKey represents a validated API key with its metadata.
type AuthenticatedKey struct {
	ID          string
	KeyPrefix   string
	Name        string
	Tier        string
	Constraints *database.KeyConstraints
}

// Create generates and stores a new API key.
// Returns the full key (show once to user) and the stored record.
func (r *Repository) Create(ctx context.Context, name, tier string, constraints *database.KeyConstraints) (*database.APIKey, string, error) {
	// Generate new API key
	fullKey, err := r.hasher.GenerateAPIKey(tier)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate API key: %w", err)
	}

	// Generate key ID
	keyID, err := crypto.GenerateAPIKeyID()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate key ID: %w", err)
	}

	// Hash the key for storage
	keyHash := r.hasher.HashAPIKey(fullKey)
	keyPrefix := crypto.GetKeyPrefix(fullKey)

	// Serialize constraints to JSON
	var constraintsJSON []byte
	if constraints != nil {
		constraintsJSON, err = json.Marshal(constraints)
		if err != nil {
			return nil, "", fmt.Errorf("failed to serialize constraints: %w", err)
		}
	}

	// Insert into database
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, key_hash, key_prefix, name, tier, constraints)
		VALUES (?, ?, ?, ?, ?, ?)
	`, keyID, keyHash, keyPrefix, name, tier, constraintsJSON)

	if err != nil {
		return nil, "", fmt.Errorf("failed to insert API key: %w", err)
	}

	// Return the stored key and the full key (for display once)
	apiKey := &database.APIKey{
		ID:          keyID,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Name:        name,
		Tier:        tier,
		Constraints: constraints,
		CreatedAt:   time.Now(),
	}

	return apiKey, fullKey, nil
}

// Authenticate validates an API key and returns its metadata.
func (r *Repository) Authenticate(ctx context.Context, key string) (*AuthenticatedKey, error) {
	// Validate key format first
	tier := crypto.ParseAPIKeyTier(key)
	if tier == "" {
		return nil, fmt.Errorf("invalid API key format")
	}

	// Hash the key
	keyHash := r.hasher.HashAPIKey(key)

	// Look up in database
	var (
		id              string
		keyPrefix       string
		name            string
		storedTier      string
		constraintsJSON sql.NullString
		expiresAt       sql.NullTime
		revokedAt       sql.NullTime
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id, key_prefix, name, tier, constraints, expires_at, revoked_at
		FROM api_keys
		WHERE key_hash = ?
	`, keyHash).Scan(&id, &keyPrefix, &name, &storedTier, &constraintsJSON, &expiresAt, &revokedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("API key not found")
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check if revoked
	if revokedAt.Valid {
		return nil, fmt.Errorf("API key has been revoked")
	}

	// Check if expired
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, fmt.Errorf("API key has expired")
	}

	// Parse constraints
	var constraints *database.KeyConstraints
	if constraintsJSON.Valid && constraintsJSON.String != "" {
		constraints = &database.KeyConstraints{}
		if err := json.Unmarshal([]byte(constraintsJSON.String), constraints); err != nil {
			return nil, fmt.Errorf("failed to parse constraints: %w", err)
		}
	}

	return &AuthenticatedKey{
		ID:          id,
		KeyPrefix:   keyPrefix,
		Name:        name,
		Tier:        storedTier,
		Constraints: constraints,
	}, nil
}

// GetByID retrieves an API key by its ID.
func (r *Repository) GetByID(ctx context.Context, id string) (*database.APIKey, error) {
	var (
		keyHash           string
		keyPrefix         string
		name              string
		tier              string
		constraintsJSON   sql.NullString
		createdAt         time.Time
		lastUsedAt        sql.NullTime
		expiresAt         sql.NullTime
		revokedAt         sql.NullTime
		rateLimitOverride sql.NullInt64
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT key_hash, key_prefix, name, tier, constraints, created_at,
		       last_used_at, expires_at, revoked_at, rate_limit_override
		FROM api_keys
		WHERE id = ?
	`, id).Scan(
		&keyHash, &keyPrefix, &name, &tier, &constraintsJSON,
		&createdAt, &lastUsedAt, &expiresAt, &revokedAt, &rateLimitOverride,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Parse constraints
	var constraints *database.KeyConstraints
	if constraintsJSON.Valid && constraintsJSON.String != "" {
		constraints = &database.KeyConstraints{}
		json.Unmarshal([]byte(constraintsJSON.String), constraints)
	}

	return &database.APIKey{
		ID:                id,
		KeyHash:           keyHash,
		KeyPrefix:         keyPrefix,
		Name:              name,
		Tier:              tier,
		Constraints:       constraints,
		CreatedAt:         createdAt,
		LastUsedAt:        lastUsedAt,
		ExpiresAt:         expiresAt,
		RevokedAt:         revokedAt,
		RateLimitOverride: rateLimitOverride,
	}, nil
}

// List returns all API keys (active and revoked).
func (r *Repository) List(ctx context.Context, includeRevoked bool) ([]database.APIKey, error) {
	query := `
		SELECT id, key_hash, key_prefix, name, tier, constraints, created_at,
		       last_used_at, expires_at, revoked_at, rate_limit_override
		FROM api_keys
	`
	if !includeRevoked {
		query += " WHERE revoked_at IS NULL"
	}
	query += " ORDER BY created_at DESC"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}
	defer rows.Close()

	var keys []database.APIKey
	for rows.Next() {
		var (
			id                string
			keyHash           string
			keyPrefix         string
			name              string
			tier              string
			constraintsJSON   sql.NullString
			createdAt         time.Time
			lastUsedAt        sql.NullTime
			expiresAt         sql.NullTime
			revokedAt         sql.NullTime
			rateLimitOverride sql.NullInt64
		)

		if err := rows.Scan(
			&id, &keyHash, &keyPrefix, &name, &tier, &constraintsJSON,
			&createdAt, &lastUsedAt, &expiresAt, &revokedAt, &rateLimitOverride,
		); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		var constraints *database.KeyConstraints
		if constraintsJSON.Valid && constraintsJSON.String != "" {
			constraints = &database.KeyConstraints{}
			json.Unmarshal([]byte(constraintsJSON.String), constraints)
		}

		keys = append(keys, database.APIKey{
			ID:                id,
			KeyHash:           keyHash,
			KeyPrefix:         keyPrefix,
			Name:              name,
			Tier:              tier,
			Constraints:       constraints,
			CreatedAt:         createdAt,
			LastUsedAt:        lastUsedAt,
			ExpiresAt:         expiresAt,
			RevokedAt:         revokedAt,
			RateLimitOverride: rateLimitOverride,
		})
	}

	return keys, rows.Err()
}

// Revoke marks an API key as revoked.
func (r *Repository) Revoke(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_keys
		SET revoked_at = datetime('now')
		WHERE id = ? AND revoked_at IS NULL
	`, id)

	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("API key not found or already revoked")
	}

	return nil
}

// UpdateLastUsed updates the last_used_at timestamp.
func (r *Repository) UpdateLastUsed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE api_keys
		SET last_used_at = datetime('now')
		WHERE id = ?
	`, id)
	return err
}

// UpdateConstraints updates the constraints for an API key.
func (r *Repository) UpdateConstraints(ctx context.Context, id string, constraints *database.KeyConstraints) error {
	constraintsJSON, err := json.Marshal(constraints)
	if err != nil {
		return fmt.Errorf("failed to serialize constraints: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE api_keys
		SET constraints = ?
		WHERE id = ?
	`, constraintsJSON, id)

	return err
}

// Count returns the count of API keys by tier.
func (r *Repository) Count(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT tier, COUNT(*) as count
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
