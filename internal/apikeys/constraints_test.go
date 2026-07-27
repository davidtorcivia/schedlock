package apikeys

import (
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
)

func writeKey(constraints *database.KeyConstraints) *AuthenticatedKey {
	return &AuthenticatedKey{ID: "key_1", Tier: database.TierWrite, Constraints: constraints}
}

func createOp() Operation {
	start := time.Now().Add(time.Hour)
	return Operation{
		Name:       database.OperationCreateEvent,
		CalendarID: "primary",
		Start:      start,
		End:        start.Add(time.Hour),
		TimesKnown: true,
	}
}

func TestTierDefaults(t *testing.T) {
	tests := []struct {
		tier      string
		operation string
		want      ConstraintResult
	}{
		{database.TierRead, database.OperationCreateEvent, ConstraintDeny},
		{database.TierRead, database.OperationDeleteEvent, ConstraintDeny},
		{database.TierWrite, database.OperationCreateEvent, ConstraintRequireApproval},
		{database.TierWrite, database.OperationUpdateEvent, ConstraintRequireApproval},
		{database.TierAdmin, database.OperationCreateEvent, ConstraintAllow},
		{"unrecognised", database.OperationCreateEvent, ConstraintRequireApproval},
	}

	for _, tt := range tests {
		op := createOp()
		op.Name = tt.operation
		got, _ := Evaluate(&AuthenticatedKey{Tier: tt.tier}, op)
		if got != tt.want {
			t.Errorf("tier %s / %s = %v, want %v", tt.tier, tt.operation, got, tt.want)
		}
	}
}

// TestAutoPolicyTakesEffect covers a constraint that used to be accepted and
// then silently ignored: a key configured to run an operation without approval
// still fell through to the tier default and waited for a human.
func TestAutoPolicyTakesEffect(t *testing.T) {
	key := writeKey(&database.KeyConstraints{
		Operations: map[string]string{
			database.OperationCreateEvent: database.OperationPolicyAuto,
		},
	})

	got, violation := Evaluate(key, createOp())
	if got != ConstraintAllow {
		t.Errorf("an explicit auto policy = %v, want ConstraintAllow (violation: %+v)", got, violation)
	}

	// An operation without an explicit policy keeps the tier default.
	op := createOp()
	op.Name = database.OperationDeleteEvent
	if got, _ := Evaluate(key, op); got != ConstraintRequireApproval {
		t.Errorf("unlisted operation = %v, want ConstraintRequireApproval", got)
	}
}

func TestDenyPolicyOverridesEverything(t *testing.T) {
	key := writeKey(&database.KeyConstraints{
		Operations: map[string]string{
			database.OperationDeleteEvent: database.OperationPolicyDeny,
		},
	})

	op := createOp()
	op.Name = database.OperationDeleteEvent

	got, violation := Evaluate(key, op)
	if got != ConstraintDeny {
		t.Fatalf("denied operation = %v, want ConstraintDeny", got)
	}
	if violation == nil || violation.Constraint != "operation" {
		t.Errorf("expected an operation violation, got %+v", violation)
	}
}

// TestCalendarAllowlistWildcard checks that the allowlist means the same thing
// in policy evaluation as it does when filtering read results; the two used
// separate implementations that disagreed about "*".
func TestCalendarAllowlistWildcard(t *testing.T) {
	key := writeKey(&database.KeyConstraints{CalendarAllowlist: []string{"*"}})

	op := createOp()
	op.CalendarID = "anything@group.calendar.google.com"

	if got, violation := Evaluate(key, op); got == ConstraintDeny {
		t.Errorf("a wildcard allowlist rejected %s: %+v", op.CalendarID, violation)
	}
	if !CalendarAllowed(op.CalendarID, []string{"*"}) {
		t.Error("CalendarAllowed disagrees with Evaluate about the wildcard")
	}
}

func TestCalendarAllowlistRejectsOthers(t *testing.T) {
	key := writeKey(&database.KeyConstraints{CalendarAllowlist: []string{"work@example.com"}})

	op := createOp()
	op.CalendarID = "personal@example.com"

	got, violation := Evaluate(key, op)
	if got != ConstraintDeny {
		t.Fatalf("an unlisted calendar = %v, want ConstraintDeny", got)
	}
	if violation == nil || violation.Constraint != "calendar_allowlist" {
		t.Errorf("expected a calendar_allowlist violation, got %+v", violation)
	}
}

func TestMaxDurationAndAttendees(t *testing.T) {
	key := writeKey(&database.KeyConstraints{MaxDurationMinutes: 30, MaxAttendees: 2})

	long := createOp()
	long.End = long.Start.Add(2 * time.Hour)
	if got, _ := Evaluate(key, long); got != ConstraintDeny {
		t.Errorf("an over-long event = %v, want ConstraintDeny", got)
	}

	crowded := createOp()
	crowded.End = crowded.Start.Add(15 * time.Minute)
	crowded.Attendees = []string{"a@x.com", "b@x.com", "c@x.com"}
	if got, _ := Evaluate(key, crowded); got != ConstraintDeny {
		t.Errorf("too many attendees = %v, want ConstraintDeny", got)
	}
}

// TestUnknownTimesRequireApproval covers the fail-closed path: when a partial
// update's effective times cannot be resolved, a duration limit must not be
// treated as satisfied.
func TestUnknownTimesRequireApproval(t *testing.T) {
	key := writeKey(&database.KeyConstraints{
		MaxDurationMinutes: 30,
		Operations: map[string]string{
			database.OperationUpdateEvent: database.OperationPolicyAuto,
		},
	})

	op := Operation{
		Name:       database.OperationUpdateEvent,
		CalendarID: "primary",
		TimesKnown: false,
	}

	if got, _ := Evaluate(key, op); got != ConstraintRequireApproval {
		t.Errorf("an unresolvable update = %v, want ConstraintRequireApproval", got)
	}
}

// TestAttendeeDomainsExamineEveryAttendee covers an ordering bug: evaluation
// returned on the first external attendee, so a denied domain later in the list
// was never reached.
func TestAttendeeDomainsExamineEveryAttendee(t *testing.T) {
	deny := false
	key := writeKey(&database.KeyConstraints{
		AttendeeDomainAllowlist: []string{"example.com"},
		AllowExternalAttendees:  &deny,
	})

	op := createOp()
	op.Attendees = []string{"colleague@example.com", "outsider@elsewhere.test"}

	got, violation := Evaluate(key, op)
	if got != ConstraintDeny {
		t.Fatalf("an attendee outside the allowed domains = %v, want ConstraintDeny", got)
	}
	if violation == nil || violation.Constraint != "attendee_domain" {
		t.Errorf("expected an attendee_domain violation, got %+v", violation)
	}
}

func TestExternalAttendeesRequireApprovalByDefault(t *testing.T) {
	key := writeKey(&database.KeyConstraints{
		AttendeeDomainAllowlist: []string{"example.com"},
	})

	op := createOp()
	op.Attendees = []string{"outsider@elsewhere.test"}

	if got, _ := Evaluate(key, op); got != ConstraintRequireApproval {
		t.Errorf("an external attendee = %v, want ConstraintRequireApproval", got)
	}
}

func TestAllDayEventsCanBeBlocked(t *testing.T) {
	key := writeKey(&database.KeyConstraints{BlockAllDayEvents: true})

	op := createOp()
	op.AllDay = true

	got, violation := Evaluate(key, op)
	if got != ConstraintDeny {
		t.Fatalf("an all-day event = %v, want ConstraintDeny", got)
	}
	if violation == nil || violation.Constraint != "all_day_events" {
		t.Errorf("expected an all_day_events violation, got %+v", violation)
	}
}

func TestNilKeyIsDenied(t *testing.T) {
	if got, _ := Evaluate(nil, createOp()); got != ConstraintDeny {
		t.Errorf("a missing key = %v, want ConstraintDeny", got)
	}
}
