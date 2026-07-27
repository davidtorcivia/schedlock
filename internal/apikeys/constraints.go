// Package apikeys provides per-key policy evaluation.
package apikeys

import (
	"fmt"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
)

// ConstraintResult is the decision produced by evaluating a key's policy.
type ConstraintResult int

const (
	// ConstraintAllow executes the operation without human approval.
	ConstraintAllow ConstraintResult = iota
	// ConstraintRequireApproval holds the operation for human approval.
	ConstraintRequireApproval
	// ConstraintDeny rejects the operation outright.
	ConstraintDeny
)

// ConstraintViolation explains which policy rejected an operation.
type ConstraintViolation struct {
	Constraint string
	Message    string
}

// Operation describes the calendar operation being evaluated.
type Operation struct {
	Name       string
	CalendarID string
	Attendees  []string
	Start      time.Time
	End        time.Time
	// AllDay marks an operation the caller has identified as an all-day event.
	AllDay bool
	// TimesKnown is false when the effective start/end could not be determined
	// (for example a partial update whose existing event could not be read).
	TimesKnown bool
}

// Evaluate applies a key's constraints to an operation.
//
// Evaluation is fail-closed: anything the policy cannot conclusively allow ends
// up requiring human approval rather than executing silently.
func Evaluate(authKey *AuthenticatedKey, op Operation) (ConstraintResult, *ConstraintViolation) {
	if authKey == nil {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "authentication",
			Message:    "No authenticated API key",
		}
	}

	tierResult := tierDefault(authKey.Tier, op.Name)
	if tierResult == ConstraintDeny {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "tier",
			Message:    fmt.Sprintf("The %s tier cannot perform %s", authKey.Tier, op.Name),
		}
	}

	constraints := authKey.Constraints
	if constraints == nil {
		return tierResult, nil
	}

	// An explicit per-operation policy is evaluated first, because "deny"
	// short-circuits everything else.
	operationPolicy := constraints.Operations[op.Name]
	if operationPolicy == database.OperationPolicyDeny {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "operation",
			Message:    fmt.Sprintf("Operation %s is not allowed for this API key", op.Name),
		}
	}

	if len(constraints.CalendarAllowlist) > 0 && !CalendarAllowed(op.CalendarID, constraints.CalendarAllowlist) {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "calendar_allowlist",
			Message:    fmt.Sprintf("Calendar %s is not in the allowed list", op.CalendarID),
		}
	}

	if constraints.MaxAttendees > 0 && len(op.Attendees) > constraints.MaxAttendees {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "max_attendees",
			Message: fmt.Sprintf("Number of attendees (%d) exceeds the maximum allowed (%d)",
				len(op.Attendees), constraints.MaxAttendees),
		}
	}

	// Duration and all-day rules can only be applied when the effective times
	// are known. When they are not, approval is required rather than assumed.
	timeChecksDeferred := false
	if constraints.MaxDurationMinutes > 0 || constraints.BlockAllDayEvents {
		if !op.TimesKnown {
			timeChecksDeferred = true
		}
	}

	if op.TimesKnown && constraints.MaxDurationMinutes > 0 {
		duration := op.End.Sub(op.Start)
		maxDuration := time.Duration(constraints.MaxDurationMinutes) * time.Minute
		if duration > maxDuration {
			return ConstraintDeny, &ConstraintViolation{
				Constraint: "max_duration",
				Message: fmt.Sprintf("Event duration (%s) exceeds the maximum allowed (%d minutes)",
					duration.Round(time.Minute), constraints.MaxDurationMinutes),
			}
		}
	}

	if op.TimesKnown && constraints.BlockAllDayEvents && isAllDay(op) {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "all_day_events",
			Message:    "All-day events are not allowed for this API key",
		}
	}

	// Every attendee is examined before returning, so a denied domain later in
	// the list is not masked by an earlier one that merely needs approval.
	externalAttendee := ""
	if len(constraints.AttendeeDomainAllowlist) > 0 {
		allowExternal := constraints.AllowExternalAttendees == nil || *constraints.AllowExternalAttendees
		for _, attendee := range op.Attendees {
			if emailInDomains(attendee, constraints.AttendeeDomainAllowlist) {
				continue
			}
			if !allowExternal {
				return ConstraintDeny, &ConstraintViolation{
					Constraint: "attendee_domain",
					Message:    fmt.Sprintf("Attendee %s is not in an allowed domain", attendee),
				}
			}
			if externalAttendee == "" {
				externalAttendee = attendee
			}
		}
	}

	if externalAttendee != "" || timeChecksDeferred {
		return ConstraintRequireApproval, nil
	}

	switch operationPolicy {
	case database.OperationPolicyRequireApproval:
		return ConstraintRequireApproval, nil
	case database.OperationPolicyAuto:
		// An explicit auto policy is the one case that may relax the tier
		// default, and only after every other constraint above has passed.
		return ConstraintAllow, nil
	}

	return tierResult, nil
}

// tierDefault is the baseline policy for a tier when a key sets no explicit
// per-operation policy.
func tierDefault(tier, operation string) ConstraintResult {
	isWrite := operation == database.OperationCreateEvent ||
		operation == database.OperationUpdateEvent ||
		operation == database.OperationDeleteEvent

	switch tier {
	case database.TierRead:
		if isWrite {
			return ConstraintDeny
		}
		return ConstraintAllow
	case database.TierWrite:
		if isWrite {
			return ConstraintRequireApproval
		}
		return ConstraintAllow
	case database.TierAdmin:
		return ConstraintAllow
	default:
		return ConstraintRequireApproval
	}
}

// CalendarAllowed reports whether a calendar is permitted by an allowlist.
// A bare "*" entry permits every calendar.
func CalendarAllowed(calendarID string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if allowed == "*" || allowed == calendarID {
			return true
		}
	}
	return false
}

// isAllDay reports whether an operation covers whole days.
func isAllDay(op Operation) bool {
	if op.AllDay {
		return true
	}
	// Fall back to the shape of the interval for callers that cannot say.
	return op.End.Sub(op.Start) >= 24*time.Hour
}

func emailInDomains(email string, domains []string) bool {
	_, domain, found := strings.Cut(email, "@")
	if !found {
		return false
	}
	domain = strings.ToLower(domain)
	for _, allowed := range domains {
		if strings.EqualFold(strings.TrimPrefix(allowed, "@"), domain) {
			return true
		}
	}
	return false
}
