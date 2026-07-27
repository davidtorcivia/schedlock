package tokens

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

func newTestRepo(t *testing.T) (*Repository, *database.DB) {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mustExec(t, db, `INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'n', 'write')`)
	mustExec(t, db, `INSERT INTO requests (id, api_key_id, operation, status, payload, expires_at)
		VALUES ('req_1', 'key_1', 'create_event', 'pending_approval', '{}', ?)`,
		util.SQLiteTimestamp(time.Now().Add(time.Hour)))

	return NewRepository(db), db
}

func mustExec(t *testing.T, db *database.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec failed: %v", err)
	}
}

func TestCreateAndConsume(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	token, err := repo.Create(ctx, "req_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(token) < 20 {
		t.Errorf("token looks too short: %q", token)
	}

	requestID, err := repo.Consume(ctx, token, ActionApprove)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if requestID != "req_1" {
		t.Errorf("Consume returned %q, want req_1", requestID)
	}
}

// TestTokenIsSingleUse is the property the whole approval-link design rests on:
// a link that reaches a chat history, a log, or a link previewer must not be
// usable twice.
func TestTokenIsSingleUse(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	token, err := repo.Create(ctx, "req_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := repo.Consume(ctx, token, ActionApprove); err != nil {
		t.Fatalf("first Consume failed: %v", err)
	}

	_, err = repo.Consume(ctx, token, ActionApprove)
	if !errors.Is(err, ErrTokenConsumed) {
		t.Errorf("second Consume returned %v, want ErrTokenConsumed", err)
	}

	// A different action on a spent token is refused for the same reason.
	if _, err := repo.Consume(ctx, token, ActionDeny); !errors.Is(err, ErrTokenConsumed) {
		t.Errorf("Consume with a different action returned %v, want ErrTokenConsumed", err)
	}
}

// TestConcurrentConsumeElectsOneWinner covers the race between an approval
// arriving from two places at once, for example a notification action tapped
// while the same link is open in a browser.
func TestConcurrentConsumeElectsOneWinner(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	token, err := repo.Create(ctx, "req_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	const attempts = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
	)

	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := repo.Consume(ctx, token, ActionApprove); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Errorf("%d concurrent consumers succeeded, want exactly 1", successes)
	}
}

func TestExpiredTokenIsRefused(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	token, err := repo.Create(ctx, "req_1", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := repo.Validate(ctx, token); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("Validate returned %v, want ErrTokenExpired", err)
	}
	if _, err := repo.Consume(ctx, token, ActionApprove); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("Consume returned %v, want ErrTokenExpired", err)
	}
}

func TestUnknownTokenIsRefused(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Consume(ctx, "dtok_doesnotexist", ActionApprove); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Consume returned %v, want ErrTokenNotFound", err)
	}
}

// TestTokenIsStoredHashed checks that a database disclosure does not hand an
// attacker working approval links.
func TestTokenIsStoredHashed(t *testing.T) {
	repo, db := newTestRepo(t)
	ctx := context.Background()

	token, err := repo.Create(ctx, "req_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT token_hash FROM decision_tokens`).Scan(&stored); err != nil {
		t.Fatalf("failed to read the stored token: %v", err)
	}
	if stored == token {
		t.Error("the token is stored verbatim; only its hash should be persisted")
	}
}

func TestDeleteExpired(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Create(ctx, "req_1", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	live, err := repo.Create(ctx, "req_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	removed, err := repo.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}
	if removed != 1 {
		t.Errorf("DeleteExpired removed %d tokens, want 1", removed)
	}

	if _, err := repo.Validate(ctx, live); err != nil {
		t.Errorf("the live token was affected: %v", err)
	}
}
