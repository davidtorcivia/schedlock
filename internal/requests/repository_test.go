package requests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	repo := NewRepository(db)
	return repo, db
}

func TestRepository_Create(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	payload := json.RawMessage(`{"summary": "Test Event"}`)
	createReq := &CreateRequest{
		APIKeyID:  "key_test123",
		Operation: database.OperationCreateEvent,
		Payload:   payload,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	req, err := repo.Create(ctx, createReq)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if req == nil {
		t.Fatal("Request is nil")
	}
	if req.APIKeyID != "key_test123" {
		t.Errorf("APIKeyID mismatch: got %q, want %q", req.APIKeyID, "key_test123")
	}
	if req.Operation != database.OperationCreateEvent {
		t.Errorf("Operation mismatch: got %q, want %q", req.Operation, database.OperationCreateEvent)
	}
	if req.Status != database.StatusPendingApproval {
		t.Errorf("Status mismatch: got %q, want %q", req.Status, database.StatusPendingApproval)
	}
}

func TestRepository_GetByID(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a request
	createReq := &CreateRequest{
		APIKeyID:  "key_test123",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{"summary": "Test"}`),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	created, _ := repo.Create(ctx, createReq)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved request is nil")
	}
	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", retrieved.ID, created.ID)
	}
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, err := repo.GetByID(ctx, "req_nonexistent1234")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}
	if req != nil {
		t.Fatal("Expected nil for non-existent request")
	}
}

func TestRepository_GetByAPIKeyID(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create requests for different API keys
	repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_a",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_a",
		Operation: database.OperationUpdateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_b",
		Operation: database.OperationDeleteEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Get requests for key_a
	requests, err := repo.GetByAPIKeyID(ctx, "key_a", 10)
	if err != nil {
		t.Fatalf("GetByAPIKeyID failed: %v", err)
	}

	if len(requests) != 2 {
		t.Errorf("Expected 2 requests, got %d", len(requests))
	}

	// All should be for key_a
	for _, req := range requests {
		if req.APIKeyID != "key_a" {
			t.Errorf("Got request for wrong API key: %s", req.APIKeyID)
		}
	}
}

func TestRepository_GetPending(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create pending request
	req1, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_a",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Create and approve another request
	req2, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_b",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	repo.UpdateStatus(ctx, req2.ID, database.StatusApproved, "test")

	// Get pending
	pending, err := repo.GetPending(ctx)
	if err != nil {
		t.Fatalf("GetPending failed: %v", err)
	}

	if len(pending) != 1 {
		t.Errorf("Expected 1 pending request, got %d", len(pending))
	}

	if pending[0].ID != req1.ID {
		t.Errorf("Wrong pending request returned")
	}
}

func TestRepository_UpdateStatus(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Update status to approved
	updated, err := repo.UpdateStatus(ctx, req.ID, database.StatusApproved, "admin")
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	if !updated {
		t.Fatal("UpdateStatus returned false")
	}

	// Verify status
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusApproved {
		t.Errorf("Status not updated: got %q, want %q", retrieved.Status, database.StatusApproved)
	}
	if !retrieved.DecidedBy.Valid || retrieved.DecidedBy.String != "admin" {
		t.Errorf("DecidedBy not set correctly: got %q, want %q", retrieved.DecidedBy.String, "admin")
	}
}

func TestRepository_UpdateStatus_AlreadyTransitioned(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// First approval succeeds
	repo.UpdateStatus(ctx, req.ID, database.StatusApproved, "admin1")

	// Second approval fails (request already transitioned)
	updated, err := repo.UpdateStatus(ctx, req.ID, database.StatusDenied, "admin2")
	if err != nil {
		t.Fatalf("UpdateStatus returned error: %v", err)
	}
	if updated {
		t.Fatal("UpdateStatus should return false for already transitioned request")
	}

	// Status should still be approved
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusApproved {
		t.Errorf("Status changed incorrectly: got %q, want %q", retrieved.Status, database.StatusApproved)
	}
}

func TestRepository_SetSuggestion(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Set suggestion
	err := repo.SetSuggestion(ctx, req.ID, "Please change the time to 3pm", "approver@example.com")
	if err != nil {
		t.Fatalf("SetSuggestion failed: %v", err)
	}

	// Verify
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusChangeRequested {
		t.Errorf("Status not changed to change_requested: got %q", retrieved.Status)
	}
	if !retrieved.SuggestionText.Valid || retrieved.SuggestionText.String != "Please change the time to 3pm" {
		t.Errorf("SuggestionText mismatch: got %q", retrieved.SuggestionText.String)
	}
	if !retrieved.SuggestionBy.Valid || retrieved.SuggestionBy.String != "approver@example.com" {
		t.Errorf("SuggestionBy mismatch: got %q", retrieved.SuggestionBy.String)
	}
}

func TestRepository_SetResult(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	result := json.RawMessage(`{"event_id": "evt_123", "url": "https://calendar.google.com/..."}`)

	err := repo.SetResult(ctx, req.ID, result)
	if err != nil {
		t.Fatalf("SetResult failed: %v", err)
	}

	// Verify
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusCompleted {
		t.Errorf("Status not changed to completed: got %q", retrieved.Status)
	}
	if string(retrieved.Result) != string(result) {
		t.Errorf("Result mismatch: got %s", retrieved.Result)
	}
}

func TestRepository_SetError(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	err := repo.SetError(ctx, req.ID, "Calendar API returned 500")
	if err != nil {
		t.Fatalf("SetError failed: %v", err)
	}

	// Verify
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusFailed {
		t.Errorf("Status not changed to failed: got %q", retrieved.Status)
	}
	if !retrieved.Error.Valid || retrieved.Error.String != "Calendar API returned 500" {
		t.Errorf("Error mismatch: got %q", retrieved.Error.String)
	}
}

func TestRepository_Cancel(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_owner",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Cancel by the owning key
	err := repo.Cancel(ctx, req.ID, "key_owner")
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	// Verify
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusCancelled {
		t.Errorf("Status not changed to cancelled: got %q", retrieved.Status)
	}
}

func TestRepository_Cancel_WrongOwner(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_owner",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Try to cancel with different key
	err := repo.Cancel(ctx, req.ID, "key_other")
	if err == nil {
		t.Fatal("Expected error when cancelling with wrong owner")
	}

	// Status should be unchanged
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.Status != database.StatusPendingApproval {
		t.Errorf("Status should not have changed: got %q", retrieved.Status)
	}
}

func TestRepository_IdempotencyKey(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create a request and store idempotency key
	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	err := repo.StoreIdempotencyKey(ctx, "key_test", "idem_abc123", req.ID)
	if err != nil {
		t.Fatalf("StoreIdempotencyKey failed: %v", err)
	}

	// Find by idempotency key
	found, err := repo.FindByIdempotencyKey(ctx, "key_test", "idem_abc123")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey failed: %v", err)
	}
	if found == nil {
		t.Fatal("Request not found by idempotency key")
	}
	if found.ID != req.ID {
		t.Errorf("Found wrong request: got %q, want %q", found.ID, req.ID)
	}
}

func TestRepository_IdempotencyKey_WrongAPIKey(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_a",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	repo.StoreIdempotencyKey(ctx, "key_a", "idem_123", req.ID)

	// Try to find with different API key
	found, _ := repo.FindByIdempotencyKey(ctx, "key_b", "idem_123")
	if found != nil {
		t.Fatal("Should not find idempotency key for different API key")
	}
}

func TestRepository_GetStats(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Create some requests in different states
	req1, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	req3, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Approve one and complete it
	repo.UpdateStatus(ctx, req1.ID, database.StatusApproved, "admin")
	repo.SetResult(ctx, req1.ID, json.RawMessage(`{}`))

	// Deny another
	repo.UpdateStatus(ctx, req3.ID, database.StatusDenied, "admin")

	stats, err := repo.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if stats.TotalPending != 1 {
		t.Errorf("TotalPending wrong: got %d, want 1", stats.TotalPending)
	}
	if stats.TotalToday != 3 {
		t.Errorf("TotalToday wrong: got %d, want 3", stats.TotalToday)
	}
}

func TestRepository_IncrementRetryCount(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Approve first
	repo.UpdateStatus(ctx, req.ID, database.StatusApproved, "admin")

	// Increment retry count
	err := repo.IncrementRetryCount(ctx, req.ID)
	if err != nil {
		t.Fatalf("IncrementRetryCount failed: %v", err)
	}

	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.RetryCount != 1 {
		t.Errorf("RetryCount wrong: got %d, want 1", retrieved.RetryCount)
	}
}

func TestRepository_SetWebhookNotified(t *testing.T) {
	repo, db := setupTestRepo(t)
	defer db.Close()

	ctx := context.Background()

	req, _ := repo.Create(ctx, &CreateRequest{
		APIKeyID:  "key_test",
		Operation: database.OperationCreateEvent,
		Payload:   json.RawMessage(`{}`),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Initially not notified
	retrieved, _ := repo.GetByID(ctx, req.ID)
	if retrieved.WebhookNotifiedAt.Valid {
		t.Fatal("WebhookNotifiedAt should not be set initially")
	}

	// Mark as notified
	err := repo.SetWebhookNotified(ctx, req.ID)
	if err != nil {
		t.Fatalf("SetWebhookNotified failed: %v", err)
	}

	retrieved, _ = repo.GetByID(ctx, req.ID)
	if !retrieved.WebhookNotifiedAt.Valid {
		t.Fatal("WebhookNotifiedAt should be set after notification")
	}
}
