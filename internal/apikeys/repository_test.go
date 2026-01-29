package apikeys

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
)

// setupTestDB creates an in-memory test database with the required schema.
func setupTestDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	return db
}

// setupTestRepo creates a test repository with an in-memory database.
func setupTestRepo(t *testing.T) (*Repository, *database.DB) {
	t.Helper()

	db := setupTestDB(t)

	hasher, err := crypto.NewAPIKeyHasher("test-secret-key-12345")
	if err != nil {
		t.Fatalf("Failed to create hasher: %v", err)
	}

	repo := NewRepository(db, hasher)
	return repo, db
}

func TestRepository_Create(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a new API key
	apiKey, fullKey, err := repo.Create(ctx, "Test Key", "write", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify API key struct
	if apiKey == nil {
		t.Fatal("APIKey is nil")
	}
	if apiKey.Name != "Test Key" {
		t.Errorf("Name mismatch: got %q, want %q", apiKey.Name, "Test Key")
	}
	if apiKey.Tier != "write" {
		t.Errorf("Tier mismatch: got %q, want %q", apiKey.Tier, "write")
	}
	if !strings.HasPrefix(apiKey.ID, "key_") {
		t.Errorf("ID doesn't have key_ prefix: %s", apiKey.ID)
	}

	// Verify full key format
	if !strings.HasPrefix(fullKey, "sk_write_") {
		t.Errorf("Full key doesn't have expected prefix: %s", fullKey)
	}
}

func TestRepository_Create_WithConstraints(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	constraints := &database.KeyConstraints{
		CalendarAllowlist:  []string{"primary", "work@example.com"},
		MaxDurationMinutes: 120,
		MaxAttendees:       5,
	}

	apiKey, _, err := repo.Create(ctx, "Constrained Key", "write", constraints)
	if err != nil {
		t.Fatalf("Create with constraints failed: %v", err)
	}

	// Verify constraints are stored
	if apiKey.Constraints == nil {
		t.Fatal("Constraints are nil")
	}
	if len(apiKey.Constraints.CalendarAllowlist) != 2 {
		t.Errorf("CalendarAllowlist length mismatch: got %d, want 2", len(apiKey.Constraints.CalendarAllowlist))
	}
	if apiKey.Constraints.MaxDurationMinutes != 120 {
		t.Errorf("MaxDurationMinutes mismatch: got %d, want 120", apiKey.Constraints.MaxDurationMinutes)
	}
}

func TestRepository_Authenticate_Valid(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a key first
	_, fullKey, err := repo.Create(ctx, "Auth Test Key", "read", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Authenticate with the full key
	authKey, err := repo.Authenticate(ctx, fullKey)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}

	if authKey == nil {
		t.Fatal("AuthenticatedKey is nil")
	}
	if authKey.Name != "Auth Test Key" {
		t.Errorf("Name mismatch: got %q, want %q", authKey.Name, "Auth Test Key")
	}
	if authKey.Tier != "read" {
		t.Errorf("Tier mismatch: got %q, want %q", authKey.Tier, "read")
	}
}

func TestRepository_Authenticate_InvalidKey(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Try to authenticate with a non-existent key
	_, err := repo.Authenticate(ctx, "sk_read_nonexistentkeyvalue")
	if err == nil {
		t.Fatal("Expected error for non-existent key")
	}
}

func TestRepository_Authenticate_InvalidFormat(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Try to authenticate with invalid format
	_, err := repo.Authenticate(ctx, "invalid-key-format")
	if err == nil {
		t.Fatal("Expected error for invalid key format")
	}
}

func TestRepository_Authenticate_RevokedKey(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create and revoke a key
	apiKey, fullKey, _ := repo.Create(ctx, "To Revoke", "write", nil)
	if err := repo.Revoke(ctx, apiKey.ID); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Try to authenticate with revoked key
	_, err := repo.Authenticate(ctx, fullKey)
	if err == nil {
		t.Fatal("Expected error for revoked key")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("Error should mention revoked: %v", err)
	}
}

func TestRepository_GetByID(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a key
	created, _, err := repo.Create(ctx, "GetByID Test", "admin", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved key is nil")
	}
	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", retrieved.ID, created.ID)
	}
	if retrieved.Name != "GetByID Test" {
		t.Errorf("Name mismatch: got %q, want %q", retrieved.Name, "GetByID Test")
	}
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Try to get non-existent key
	key, err := repo.GetByID(ctx, "key_nonexistent12345")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}
	if key != nil {
		t.Fatal("Expected nil for non-existent key")
	}
}

func TestRepository_List(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create multiple keys
	repo.Create(ctx, "Key 1", "read", nil)
	repo.Create(ctx, "Key 2", "write", nil)
	repo.Create(ctx, "Key 3", "admin", nil)

	// List all keys
	keys, err := repo.List(ctx, false)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(keys) != 3 {
		t.Errorf("Expected 3 keys, got %d", len(keys))
	}
}

func TestRepository_List_ExcludesRevoked(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create keys and revoke one
	key1, _, _ := repo.Create(ctx, "Active Key", "read", nil)
	key2, _, _ := repo.Create(ctx, "To Revoke", "write", nil)
	repo.Revoke(ctx, key2.ID)

	// List without revoked
	keys, _ := repo.List(ctx, false)
	if len(keys) != 1 {
		t.Errorf("Expected 1 active key, got %d", len(keys))
	}
	if keys[0].ID != key1.ID {
		t.Errorf("Wrong key returned")
	}

	// List with revoked
	allKeys, _ := repo.List(ctx, true)
	if len(allKeys) != 2 {
		t.Errorf("Expected 2 total keys, got %d", len(allKeys))
	}
}

func TestRepository_Revoke(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a key
	apiKey, _, _ := repo.Create(ctx, "To Revoke", "write", nil)

	// Revoke it
	err := repo.Revoke(ctx, apiKey.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Verify it's revoked
	retrieved, _ := repo.GetByID(ctx, apiKey.ID)
	if !retrieved.RevokedAt.Valid {
		t.Fatal("Key should be revoked")
	}
}

func TestRepository_Revoke_AlreadyRevoked(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create and revoke a key
	apiKey, _, _ := repo.Create(ctx, "To Revoke", "write", nil)
	repo.Revoke(ctx, apiKey.ID)

	// Try to revoke again
	err := repo.Revoke(ctx, apiKey.ID)
	if err == nil {
		t.Fatal("Expected error when revoking already revoked key")
	}
}

func TestRepository_Revoke_NotFound(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	err := repo.Revoke(ctx, "key_nonexistent12345")
	if err == nil {
		t.Fatal("Expected error when revoking non-existent key")
	}
}

func TestRepository_UpdateLastUsed(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a key
	apiKey, _, _ := repo.Create(ctx, "Update Test", "read", nil)

	// Initially last_used_at should not be set
	retrieved, _ := repo.GetByID(ctx, apiKey.ID)
	if retrieved.LastUsedAt.Valid {
		t.Fatal("LastUsedAt should not be set initially")
	}

	// Update last used
	time.Sleep(10 * time.Millisecond) // Ensure time difference
	err := repo.UpdateLastUsed(ctx, apiKey.ID)
	if err != nil {
		t.Fatalf("UpdateLastUsed failed: %v", err)
	}

	// Verify it's updated
	retrieved, _ = repo.GetByID(ctx, apiKey.ID)
	if !retrieved.LastUsedAt.Valid {
		t.Fatal("LastUsedAt should be set after update")
	}
}

func TestRepository_UpdateConstraints(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a key without constraints
	apiKey, _, _ := repo.Create(ctx, "Constraint Update", "write", nil)

	// Add constraints
	newConstraints := &database.KeyConstraints{
		CalendarAllowlist: []string{"work@example.com"},
		MaxAttendees:      10,
	}

	err := repo.UpdateConstraints(ctx, apiKey.ID, newConstraints)
	if err != nil {
		t.Fatalf("UpdateConstraints failed: %v", err)
	}

	// Verify constraints are updated
	retrieved, _ := repo.GetByID(ctx, apiKey.ID)
	if retrieved.Constraints == nil {
		t.Fatal("Constraints should be set")
	}
	if len(retrieved.Constraints.CalendarAllowlist) != 1 {
		t.Errorf("CalendarAllowlist length mismatch: got %d, want 1", len(retrieved.Constraints.CalendarAllowlist))
	}
}

func TestRepository_Count(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create keys of different tiers
	repo.Create(ctx, "Read 1", "read", nil)
	repo.Create(ctx, "Read 2", "read", nil)
	repo.Create(ctx, "Write 1", "write", nil)
	repo.Create(ctx, "Admin 1", "admin", nil)

	// Revoke one read key
	keys, _ := repo.List(ctx, false)
	for _, k := range keys {
		if k.Name == "Read 1" {
			repo.Revoke(ctx, k.ID)
			break
		}
	}

	// Count should exclude revoked
	counts, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	if counts["read"] != 1 {
		t.Errorf("Read count wrong: got %d, want 1", counts["read"])
	}
	if counts["write"] != 1 {
		t.Errorf("Write count wrong: got %d, want 1", counts["write"])
	}
	if counts["admin"] != 1 {
		t.Errorf("Admin count wrong: got %d, want 1", counts["admin"])
	}
}
