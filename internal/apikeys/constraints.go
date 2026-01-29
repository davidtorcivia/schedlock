// Package apikeys provides constraint evaluation for API keys.
package apikeys

import (
	"fmt"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
)

// ConstraintResult represents the result of constraint evaluation.
type ConstraintResult int

const (
	// ConstraintAllow means the operation is allowed.
	ConstraintAllow ConstraintResult = iota
	// ConstraintRequireApproval means the operation needs human approval.
	ConstraintRequireApproval
	// ConstraintDeny means the operation is denied.
	ConstraintDeny
)

// ConstraintViolation describes why a constraint was violated.
type ConstraintViolation struct {
	Constraint string
	Message    string
}

func (v ConstraintViolation) Error() string {
	return v.Message
}

// EvaluateConstraints checks if an operation is allowed based on key constraints.
// Returns the result and any violations.
func EvaluateConstraints(
	authKey *AuthenticatedKey,
	operation string,
	calendarID string,
	attendees []string,
	start, end time.Time,
) (ConstraintResult, *ConstraintViolation) {
	// If no constraints, use tier defaults
	if authKey.Constraints == nil {
		return getTierDefault(authKey.Tier, operation), nil
	}

	constraints := authKey.Constraints

	// Check operation override
	if constraints.Operations != nil {
		if action, ok := constraints.Operations[operation]; ok {
			switch action {
			case "deny":
				return ConstraintDeny, &ConstraintViolation{
					Constraint: "operation",
					Message:    fmt.Sprintf("Operation %s is not allowed for this API key", operation),
				}
			case "allow", "auto":
				// Will still check other constraints
			case "require_approval":
				// Continue checking other constraints, but will require approval
			}
		}
	}

	// Check calendar allowlist
	if len(constraints.CalendarAllowlist) > 0 {
		allowed := false
		for _, allowedCal := range constraints.CalendarAllowlist {
			if allowedCal == calendarID {
				allowed = true
				break
			}
		}
		if !allowed {
			return ConstraintDeny, &ConstraintViolation{
				Constraint: "calendar_allowlist",
				Message:    fmt.Sprintf("Calendar %s is not in the allowed list", calendarID),
			}
		}
	}

	// Check max duration
	if constraints.MaxDurationMinutes > 0 {
		duration := end.Sub(start)
		maxDuration := time.Duration(constraints.MaxDurationMinutes) * time.Minute
		if duration > maxDuration {
			return ConstraintDeny, &ConstraintViolation{
				Constraint: "max_duration",
				Message:    fmt.Sprintf("Event duration (%v) exceeds maximum allowed (%d minutes)", duration, constraints.MaxDurationMinutes),
			}
		}
	}

	// Check max attendees
	if constraints.MaxAttendees > 0 && len(attendees) > constraints.MaxAttendees {
		return ConstraintDeny, &ConstraintViolation{
			Constraint: "max_attendees",
			Message:    fmt.Sprintf("Number of attendees (%d) exceeds maximum allowed (%d)", len(attendees), constraints.MaxAttendees),
		}
	}

	// Check attendee domains
	if len(constraints.AttendeeDomainAllowlist) > 0 {
		for _, attendee := range attendees {
			if !isEmailInDomainList(attendee, constraints.AttendeeDomainAllowlist) {
				if constraints.AllowExternalAttendees != nil && !*constraints.AllowExternalAttendees {
					return ConstraintDeny, &ConstraintViolation{
						Constraint: "attendee_domain",
						Message:    fmt.Sprintf("Attendee %s is not in an allowed domain", attendee),
					}
				}
				// External attendee, require approval
				return ConstraintRequireApproval, nil
			}
		}
	}

	// Check all-day events
	if constraints.BlockAllDayEvents {
		// All-day events typically have no time component or span full days
		// For simplicity, check if duration is >= 24 hours
		if end.Sub(start) >= 24*time.Hour {
			return ConstraintDeny, &ConstraintViolation{
				Constraint: "all_day_events",
				Message:    "All-day events are not allowed for this API key",
			}
		}
	}

	// Check operation-specific setting
	if constraints.Operations != nil {
		if action, ok := constraints.Operations[operation]; ok && action == "require_approval" {
			return ConstraintRequireApproval, nil
		}
	}

	// Use tier default
	return getTierDefault(authKey.Tier, operation), nil
}

// getTierDefault returns the default constraint result for a tier and operation.
func getTierDefault(tier, operation string) ConstraintResult {
	switch tier {
	case database.TierRead:
		// Read tier cannot perform write operations
		if operation == database.OperationCreateEvent ||
			operation == database.OperationUpdateEvent ||
			operation == database.OperationDeleteEvent {
			return ConstraintDeny
		}
		return ConstraintAllow

	case database.TierWrite:
		// Write tier requires approval for write operations
		if operation == database.OperationCreateEvent ||
			operation == database.OperationUpdateEvent ||
			operation == database.OperationDeleteEvent {
			return ConstraintRequireApproval
		}
		return ConstraintAllow

	case database.TierAdmin:
		// Admin tier auto-approves everything (but still logged)
		return ConstraintAllow

	default:
		// Unknown tier, require approval
		return ConstraintRequireApproval
	}
}

// isEmailInDomainList checks if an email's domain is in the allowlist.
func isEmailInDomainList(email string, domains []string) bool {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}

	emailDomain := strings.ToLower(parts[1])
	for _, domain := range domains {
		if strings.ToLower(domain) == emailDomain {
			return true
		}
	}

	return false
}

// CanPerformOperation is a simple check if the tier allows the operation at all.
func CanPerformOperation(tier, operation string) bool {
	switch tier {
	case database.TierRead:
		// Read tier can only perform read operations
		return operation != database.OperationCreateEvent &&
			operation != database.OperationUpdateEvent &&
			operation != database.OperationDeleteEvent

	case database.TierWrite, database.TierAdmin:
		// Write and admin can perform all operations
		return true

	default:
		return false
	}
}

// RequiresApproval checks if an operation requires approval for the given tier.
func RequiresApproval(tier, operation string) bool {
	switch tier {
	case database.TierRead:
		// Read tier can't perform write operations at all
		return false

	case database.TierWrite:
		// Write tier requires approval for write operations
		return operation == database.OperationCreateEvent ||
			operation == database.OperationUpdateEvent ||
			operation == database.OperationDeleteEvent

	case database.TierAdmin:
		// Admin tier doesn't require approval
		return false

	default:
		// Unknown tier, require approval
		return true
	}
}
