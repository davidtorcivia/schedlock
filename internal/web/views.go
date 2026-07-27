package web

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

// View models keep sql.NullString and json.RawMessage out of the templates.
// Rendering a nullable column directly produces "{value true}" on the page, so
// nullability is resolved here, once, instead of in every template.

// RequestView is one request as the UI presents it.
type RequestView struct {
	ID           string
	Operation    string
	Status       string
	Summary      string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	DecidedAt    time.Time
	DecidedBy    string
	ExecutedAt   time.Time
	Error        string
	Suggestion   string
	SuggestionBy string
	SuggestionAt time.Time
	Payload      json.RawMessage
	Result       json.RawMessage
	Details      EventSummary
	IsPending    bool
	IsEditable   bool
}

// EventSummary is the human-readable description of a requested operation,
// pre-formatted in the operator's display timezone.
type EventSummary struct {
	Title       string
	StartTime   string
	EndTime     string
	Location    string
	Attendees   string
	Description string
	Calendar    string
	EventID     string
	IsPartial   bool
}

// EventForm carries the editable fields of a pending request.
type EventForm struct {
	Editable    bool
	Title       string
	Location    string
	Description string
	Start       time.Time
	End         time.Time
}

// AuditView is one audit entry as the UI presents it.
type AuditView struct {
	Timestamp time.Time
	EventType string
	RequestID string
	Actor     string
	IPAddress string
}

// newRequestView builds the view model for a request.
func newRequestView(req *database.Request) RequestView {
	details := engine.DescribeRequest(req)

	view := RequestView{
		ID:         req.ID,
		Operation:  req.Operation,
		Status:     req.Status,
		Summary:    engine.SummarizeOperation(req.Operation, details),
		CreatedAt:  req.CreatedAt,
		ExpiresAt:  req.ExpiresAt,
		DecidedBy:  req.DecidedBy.String,
		Error:      req.Error.String,
		Suggestion: req.SuggestionText.String,
		Payload:    req.Payload,
		Result:     req.Result,
		Details:    summarizeEvent(details),
		IsPending:  req.Status == database.StatusPendingApproval,
	}

	view.IsEditable = view.IsPending &&
		(req.Operation == database.OperationCreateEvent || req.Operation == database.OperationUpdateEvent)

	if req.DecidedAt.Valid {
		view.DecidedAt = req.DecidedAt.Time
	}
	if req.ExecutedAt.Valid {
		view.ExecutedAt = req.ExecutedAt.Time
	}
	if req.SuggestionAt.Valid {
		view.SuggestionAt = req.SuggestionAt.Time
	}
	view.SuggestionBy = req.SuggestionBy.String

	return view
}

// summarizeEvent formats event details for display.
func summarizeEvent(details *notifications.EventDetails) EventSummary {
	if details == nil {
		return EventSummary{}
	}

	formatter := util.GetDefaultFormatter()
	summary := EventSummary{
		Title:       details.Title,
		Location:    details.Location,
		Description: details.Description,
		Calendar:    details.CalendarID,
		EventID:     details.EventID,
		IsPartial:   details.IsPartial,
	}
	if !details.StartTime.IsZero() {
		summary.StartTime = formatter.FormatDateTime(details.StartTime)
	}
	if !details.EndTime.IsZero() {
		summary.EndTime = formatter.FormatDateTime(details.EndTime)
	}
	if len(details.Attendees) > 0 {
		summary.Attendees = strings.Join(details.Attendees, ", ")
	}
	return summary
}

// newEventForm builds the edit form model for a pending request.
func newEventForm(req *database.Request) EventForm {
	details := engine.DescribeRequest(req)
	if details == nil {
		return EventForm{}
	}

	editable := req.Status == database.StatusPendingApproval &&
		(req.Operation == database.OperationCreateEvent || req.Operation == database.OperationUpdateEvent)

	return EventForm{
		Editable:    editable,
		Title:       details.Title,
		Location:    details.Location,
		Description: details.Description,
		Start:       details.StartTime,
		End:         details.EndTime,
	}
}

// summarizeRequests builds view models for a list of requests.
func summarizeRequests(reqs []database.Request) []RequestView {
	views := make([]RequestView, 0, len(reqs))
	for i := range reqs {
		views = append(views, newRequestView(&reqs[i]))
	}
	return views
}

// summarizeAudit builds view models for audit entries.
func summarizeAudit(entries []database.AuditLogEntry) []AuditView {
	views := make([]AuditView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, AuditView{
			Timestamp: entry.Timestamp,
			EventType: entry.EventType,
			RequestID: entry.RequestID.String,
			Actor:     entry.Actor.String,
			IPAddress: entry.IPAddress.String,
		})
	}
	return views
}
