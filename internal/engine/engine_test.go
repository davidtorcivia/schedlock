package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/tokens"
)

// fakeCalendar records what the engine executed and can be made to fail.
type fakeCalendar struct {
	mu       sync.Mutex
	created  []*google.EventIntent
	deleted  []*google.EventDeleteIntent
	failWith error
	failures int
}

func (f *fakeCalendar) CreateEvent(ctx context.Context, intent *google.EventIntent) (*google.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failWith != nil && f.failures > 0 {
		f.failures--
		return nil, f.failWith
	}
	f.created = append(f.created, intent)
	return &google.Event{ID: "evt_created", Summary: intent.Summary}, nil
}

func (f *fakeCalendar) UpdateEvent(ctx context.Context, intent *google.EventUpdateIntent) (*google.Event, error) {
	return &google.Event{ID: intent.EventID}, nil
}

func (f *fakeCalendar) DeleteEvent(ctx context.Context, intent *google.EventDeleteIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, intent)
	return nil
}

func (f *fakeCalendar) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

// fakeNotifier records the notifications the engine emitted.
type fakeNotifier struct {
	mu        sync.Mutex
	approvals []*notifications.ApprovalNotification
	results   []*notifications.ResultNotification
}

func (f *fakeNotifier) SendApprovalRequest(ctx context.Context, n *notifications.ApprovalNotification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals = append(f.approvals, n)
	return nil
}

func (f *fakeNotifier) SendResult(ctx context.Context, n *notifications.ResultNotification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, n)
}

func (f *fakeNotifier) approvalCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.approvals)
}

func (f *fakeNotifier) resultStatuses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	statuses := make([]string, 0, len(f.results))
	for _, r := range f.results {
		statuses = append(statuses, r.Status)
	}
	return statuses
}

type engineFixture struct {
	engine   *Engine
	repo     *requests.Repository
	calendar *fakeCalendar
	notifier *fakeNotifier
	authKey  *apikeys.AuthenticatedKey
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`INSERT INTO api_keys (id, key_hash, key_prefix, name, tier)
		VALUES ('key_1', 'h', 'p', 'agent', 'write')`); err != nil {
		t.Fatalf("failed to seed an API key: %v", err)
	}

	cfg := &config.Config{}
	cfg.Approval.TimeoutMinutes = 60
	cfg.Retry.Enabled = true
	cfg.Retry.MaxAttempts = 3
	cfg.Retry.BackoffSeconds = []int{0}
	cfg.Retry.RetryableStatusCodes = []int{503}

	repo := requests.NewRepository(db)
	calendar := &fakeCalendar{}
	notifier := &fakeNotifier{}

	eng := NewEngine(cfg, repo, calendar, NewAuditLogger(db), tokens.NewRepository(db))
	eng.SetNotifier(notifier)

	return &engineFixture{
		engine:   eng,
		repo:     repo,
		calendar: calendar,
		notifier: notifier,
		authKey:  &apikeys.AuthenticatedKey{ID: "key_1", Tier: database.TierWrite},
	}
}

func createPayload(t *testing.T, summary string) []byte {
	t.Helper()
	start := time.Now().Add(time.Hour)
	return mustJSON(t, google.EventIntent{
		CalendarID: "primary",
		Summary:    summary,
		Start:      start,
		End:        start.Add(time.Hour),
	})
}

// TestApprovalIsRequiredBeforeExecution is the property the product exists to
// provide: a submitted write must not reach the calendar until a human says so.
func TestApprovalIsRequiredBeforeExecution(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Team sync"), "", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	if req.Status != database.StatusPendingApproval {
		t.Fatalf("a submitted request is %q, want pending_approval", req.Status)
	}
	if f.calendar.createdCount() != 0 {
		t.Fatal("the calendar was written to before anyone approved")
	}

	// The approver is notified. Notification delivery is asynchronous.
	waitFor(t, func() bool { return f.notifier.approvalCount() == 1 })

	f.engine.Start(ctx)
	defer f.engine.Stop(ctx)

	if err := f.engine.ProcessApproval(ctx, req.ID, "approve", "web:admin"); err != nil {
		t.Fatalf("ProcessApproval failed: %v", err)
	}

	waitFor(t, func() bool { return f.calendar.createdCount() == 1 })

	final, err := f.repo.GetByID(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if final.Status != database.StatusCompleted {
		t.Errorf("final status = %q, want completed", final.Status)
	}
}

// TestDenialNeverReachesTheCalendar is the other half of that guarantee.
func TestDenialNeverReachesTheCalendar(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	f.engine.Start(ctx)
	defer f.engine.Stop(ctx)

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Unwanted"), "", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	if err := f.engine.ProcessApproval(ctx, req.ID, "deny", "web:admin"); err != nil {
		t.Fatalf("ProcessApproval failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if f.calendar.createdCount() != 0 {
		t.Error("a denied request was written to the calendar")
	}

	final, _ := f.repo.GetByID(ctx, req.ID)
	if final.Status != database.StatusDenied {
		t.Errorf("final status = %q, want denied", final.Status)
	}
}

// TestDecisionIsSingleShot covers concurrent approvers and the timeout worker
// racing the same request.
func TestDecisionIsSingleShot(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	f.engine.Start(ctx)
	defer f.engine.Stop(ctx)

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Contested"), "", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	start := make(chan struct{})

	for i := 0; i < 6; i++ {
		wg.Add(1)
		action := "approve"
		if i%2 == 0 {
			action = "deny"
		}
		go func(action string) {
			defer wg.Done()
			<-start
			if err := f.engine.ProcessApproval(ctx, req.ID, action, "web:admin"); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}(action)
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("%d concurrent decisions succeeded, want exactly 1", succeeded)
	}

	// A second decision reports the state rather than silently doing nothing.
	err = f.engine.ProcessApproval(ctx, req.ID, "approve", "web:admin")
	var decided *ErrAlreadyDecided
	if !errors.As(err, &decided) {
		t.Errorf("a repeat decision returned %v, want ErrAlreadyDecided", err)
	}
}

// TestIdempotencyKeyReturnsTheSameRequest covers a retrying agent: the same key
// must not create a second approval request.
func TestIdempotencyKeyReturnsTheSameRequest(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	first, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Once"), "idem-1", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	second, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Once"), "idem-1", true, "policy")
	if err != nil {
		t.Fatalf("second SubmitRequest failed: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("the same idempotency key produced %s and %s", first.ID, second.ID)
	}

	pending, err := f.repo.GetPending(ctx)
	if err != nil {
		t.Fatalf("GetPending failed: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("%d pending requests, want 1", len(pending))
	}
}

// TestConcurrentIdempotentSubmissionsLeaveOneRequest covers the race the
// earlier lookup-then-insert could not: both callers passed the lookup, so both
// created a request and one was left pending forever.
func TestConcurrentIdempotentSubmissionsLeaveOneRequest(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	const callers = 5
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = map[string]struct{}{}
	)
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
				createPayload(t, "Race"), "idem-race", true, "policy")
			if err != nil {
				return
			}
			mu.Lock()
			ids[req.ID] = struct{}{}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(ids) != 1 {
		t.Errorf("concurrent submissions produced %d distinct requests, want 1", len(ids))
	}

	pending, _ := f.repo.GetPending(ctx)
	if len(pending) != 1 {
		t.Errorf("%d requests are pending, want 1 (an orphan was left behind)", len(pending))
	}
}

// TestApproverIsToldTheOutcome covers a gap in the loop: an approved request
// that failed against Google left the approver believing the change was made.
func TestApproverIsToldTheOutcome(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	f.engine.Start(ctx)
	defer f.engine.Stop(ctx)

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Reported"), "", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}
	if err := f.engine.ProcessApproval(ctx, req.ID, "approve", "web:admin"); err != nil {
		t.Fatalf("ProcessApproval failed: %v", err)
	}

	waitFor(t, func() bool {
		for _, status := range f.notifier.resultStatuses() {
			if status == database.StatusCompleted {
				return true
			}
		}
		return false
	})
}

// TestAutoApprovedRequestExecutesImmediately covers the policy path where a key
// is trusted to act without review.
func TestAutoApprovedRequestExecutesImmediately(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	f.engine.Start(ctx)
	defer f.engine.Stop(ctx)

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Trusted"), "", false, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	if req.Status == database.StatusPendingApproval {
		t.Error("an auto-approved request is still pending")
	}
	waitFor(t, func() bool { return f.calendar.createdCount() == 1 })

	if f.notifier.approvalCount() != 0 {
		t.Error("an approval notification was sent for an auto-approved request")
	}
}

func TestSuggestionMovesRequestOutOfPending(t *testing.T) {
	f := newEngineFixture(t)
	ctx := context.Background()

	req, err := f.engine.SubmitRequest(ctx, f.authKey, database.OperationCreateEvent,
		createPayload(t, "Needs work"), "", true, "policy")
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	if err := f.engine.ProcessSuggestion(ctx, req.ID, "Move it to Thursday", "web:admin"); err != nil {
		t.Fatalf("ProcessSuggestion failed: %v", err)
	}

	updated, _ := f.repo.GetByID(ctx, req.ID)
	if updated.Status != database.StatusChangeRequested {
		t.Errorf("status = %q, want change_requested", updated.Status)
	}
	if updated.SuggestionText.String != "Move it to Thursday" {
		t.Errorf("suggestion = %q", updated.SuggestionText.String)
	}

	// An empty suggestion is rejected rather than silently resolving the request.
	if err := f.engine.ProcessSuggestion(ctx, req.ID, "   ", "web:admin"); err == nil {
		t.Error("an empty suggestion was accepted")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the expected state")
}
