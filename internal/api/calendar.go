package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/util"
)

// maxListResults caps how many events one page may return.
const maxListResults = 250

// ListCalendars returns the calendars this key may see.
func (h *Handler) ListCalendars(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	calendars, err := h.calendarClient.ListCalendars(r.Context())
	if err != nil {
		response.WriteUpstreamError(w, "Failed to list calendars", err)
		return
	}

	// A key restricted to specific calendars must not learn that the others
	// exist.
	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		filtered := make([]google.Calendar, 0, len(calendars))
		for _, cal := range calendars {
			if apikeys.CalendarAllowed(cal.ID, authKey.Constraints.CalendarAllowlist) {
				filtered = append(filtered, cal)
			}
		}
		calendars = filtered
	}

	response.JSON(w, http.StatusOK, map[string]any{"calendars": calendars})
}

// ListEvents returns events from one calendar.
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	calendarID := r.PathValue("calendarId")
	if !h.calendarPermitted(w, authKey, calendarID) {
		return
	}

	query := r.URL.Query()

	timeMin := time.Now()
	if raw := query.Get("timeMin"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.WriteValidationError(w, "invalid timeMin (expected RFC3339)")
			return
		}
		timeMin = parsed
	}

	timeMax := timeMin.AddDate(0, 1, 0)
	if raw := query.Get("timeMax"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.WriteValidationError(w, "invalid timeMax (expected RFC3339)")
			return
		}
		timeMax = parsed
	}

	if !timeMax.After(timeMin) {
		response.WriteValidationError(w, "timeMax must be after timeMin")
		return
	}

	maxResults := 50
	if raw := query.Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxListResults {
			response.WriteValidationError(w, "maxResults must be between 1 and "+strconv.Itoa(maxListResults))
			return
		}
		maxResults = parsed
	}

	singleEvents := true
	if raw := query.Get("singleEvents"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			response.WriteValidationError(w, "invalid singleEvents (expected true or false)")
			return
		}
		singleEvents = parsed
	}

	orderBy := query.Get("orderBy")
	switch orderBy {
	case "", "startTime", "updated":
	default:
		response.WriteValidationError(w, "orderBy must be startTime or updated")
		return
	}
	// Google rejects ordering by start time unless recurrences are expanded.
	if orderBy == "startTime" && !singleEvents {
		response.WriteValidationError(w, "orderBy=startTime requires singleEvents=true")
		return
	}

	events, err := h.calendarClient.ListEvents(r.Context(), google.EventListOptions{
		CalendarID:   calendarID,
		TimeMin:      timeMin,
		TimeMax:      timeMax,
		MaxResults:   maxResults,
		PageToken:    query.Get("pageToken"),
		Query:        query.Get("q"),
		SingleEvents: singleEvents,
		OrderBy:      orderBy,
	})
	if err != nil {
		response.WriteUpstreamError(w, "Failed to list events", err)
		return
	}

	body := map[string]any{"events": events.Events}
	if events.NextPageToken != "" {
		body["next_page_token"] = events.NextPageToken
	}
	response.JSON(w, http.StatusOK, body)
}

// GetEvent returns one event.
func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	calendarID := r.PathValue("calendarId")
	eventID := r.PathValue("eventId")
	if eventID == "" {
		response.WriteValidationError(w, "eventId is required")
		return
	}
	if !h.calendarPermitted(w, authKey, calendarID) {
		return
	}

	event, err := h.calendarClient.GetEvent(r.Context(), calendarID, eventID)
	if err != nil {
		response.WriteUpstreamError(w, "Failed to get event", err)
		return
	}
	if event == nil {
		response.WriteError(w, http.StatusNotFound, response.CodeRequestNotFound, "Event not found")
		return
	}

	response.JSON(w, http.StatusOK, event)
}

// FreeBusyRequest is a free/busy query body.
type FreeBusyRequest struct {
	TimeMin   time.Time `json:"timeMin"`
	TimeMax   time.Time `json:"timeMax"`
	Calendars []string  `json:"calendars"`
}

// FreeBusy reports busy intervals across calendars.
func (h *Handler) FreeBusy(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	req, err := parseFreeBusyRequest(w, r)
	if err != nil {
		response.WriteValidationError(w, err.Error())
		return
	}

	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		allowed := make([]string, 0, len(req.Calendars))
		for _, cal := range req.Calendars {
			if apikeys.CalendarAllowed(cal, authKey.Constraints.CalendarAllowlist) {
				allowed = append(allowed, cal)
			}
		}
		if len(allowed) == 0 {
			response.WriteConstraintViolation(w, "calendar_allowlist", "No requested calendar is allowed for this API key")
			return
		}
		req.Calendars = allowed
	}

	fbReq := &google.FreeBusyRequest{TimeMin: req.TimeMin, TimeMax: req.TimeMax}
	for _, cal := range req.Calendars {
		fbReq.Items = append(fbReq.Items, google.FreeBusyCalendar{ID: cal})
	}

	result, err := h.calendarClient.FreeBusy(r.Context(), fbReq)
	if err != nil {
		response.WriteUpstreamError(w, "Failed to query free/busy", err)
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// parseFreeBusyRequest accepts the query either as a JSON body or as query
// parameters, since this endpoint is reachable by both GET and POST.
func parseFreeBusyRequest(w http.ResponseWriter, r *http.Request) (*FreeBusyRequest, error) {
	req := &FreeBusyRequest{}

	if r.Method == http.MethodPost && r.ContentLength != 0 {
		if err := decodeJSON(w, r, req); err != nil {
			return nil, errors.New("invalid request body: " + err.Error())
		}
	} else {
		query := r.URL.Query()
		if raw := query.Get("timeMin"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, errors.New("invalid timeMin (expected RFC3339)")
			}
			req.TimeMin = parsed
		}
		if raw := query.Get("timeMax"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, errors.New("invalid timeMax (expected RFC3339)")
			}
			req.TimeMax = parsed
		}
		if raw := query.Get("calendars"); raw != "" {
			req.Calendars = splitAndTrim(raw)
		}
	}

	if req.TimeMin.IsZero() {
		req.TimeMin = time.Now()
	}
	if req.TimeMax.IsZero() {
		req.TimeMax = req.TimeMin.AddDate(0, 0, 7)
	}
	if !req.TimeMax.After(req.TimeMin) {
		return nil, errors.New("timeMax must be after timeMin")
	}
	if len(req.Calendars) == 0 {
		req.Calendars = []string{"primary"}
	}

	for _, cal := range req.Calendars {
		if err := util.ValidateCalendarID(cal); err != nil {
			return nil, errors.New("invalid calendar ID: " + cal)
		}
	}

	return req, nil
}

// CreateEvent submits an event creation for approval.
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierWrite)
	if authKey == nil {
		return
	}

	var intent google.EventIntent
	if err := decodeJSON(w, r, &intent); err != nil {
		response.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	intent.Sanitize()
	if err := intent.Validate(); err != nil {
		response.WriteValidationError(w, err.Error())
		return
	}

	approvalRequired, ok := h.evaluate(w, authKey, apikeys.Operation{
		Name:       database.OperationCreateEvent,
		CalendarID: intent.CalendarID,
		Attendees:  intent.Attendees,
		Start:      intent.Start,
		End:        intent.End,
		TimesKnown: true,
	})
	if !ok {
		return
	}

	h.submit(w, r, authKey, database.OperationCreateEvent, intent, approvalRequired)
}

// UpdateEvent submits an event update for approval.
func (h *Handler) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierWrite)
	if authKey == nil {
		return
	}

	var intent google.EventUpdateIntent
	if err := decodeJSON(w, r, &intent); err != nil {
		response.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	intent.Sanitize()
	if err := intent.Validate(); err != nil {
		response.WriteValidationError(w, err.Error())
		return
	}
	if !intent.HasChanges() {
		response.WriteValidationError(w, "no changes provided")
		return
	}

	op := h.updateOperation(r.Context(), authKey, &intent)
	approvalRequired, ok := h.evaluate(w, authKey, op)
	if !ok {
		return
	}

	h.submit(w, r, authKey, database.OperationUpdateEvent, intent, approvalRequired)
}

// DeleteEvent submits an event deletion for approval.
func (h *Handler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierWrite)
	if authKey == nil {
		return
	}

	var intent google.EventDeleteIntent
	if err := decodeJSON(w, r, &intent); err != nil {
		response.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := intent.Validate(); err != nil {
		response.WriteValidationError(w, err.Error())
		return
	}

	approvalRequired, ok := h.evaluate(w, authKey, apikeys.Operation{
		Name:       database.OperationDeleteEvent,
		CalendarID: intent.CalendarID,
	})
	if !ok {
		return
	}

	h.submit(w, r, authKey, database.OperationDeleteEvent, intent, approvalRequired)
}

// submit stores the request and renders the response.
func (h *Handler) submit(
	w http.ResponseWriter,
	r *http.Request,
	authKey *apikeys.AuthenticatedKey,
	operation string,
	intent any,
	approvalRequired bool,
) {
	payload, err := json.Marshal(intent)
	if err != nil {
		response.WriteInternalError(w, "Failed to record the request", err)
		return
	}

	req, err := h.engine.SubmitRequest(r.Context(), authKey, operation, payload,
		r.Header.Get("Idempotency-Key"), approvalRequired, "policy")
	if err != nil {
		response.WriteInternalError(w, "Failed to submit the request", err)
		return
	}

	status := http.StatusAccepted
	message := "Request submitted for approval"
	if !approvalRequired {
		status = http.StatusOK
		message = "Request executed without approval, as permitted by this key's policy"
	}

	response.JSON(w, status, map[string]any{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    message,
	})
}

// updateOperation resolves the effective times and attendees of an update by
// merging the requested changes over the existing event.
//
// When the existing event cannot be read, the operation is reported with
// unknown times so policy evaluation falls back to requiring approval rather
// than assuming the constraints are satisfied.
func (h *Handler) updateOperation(ctx context.Context, authKey *apikeys.AuthenticatedKey, intent *google.EventUpdateIntent) apikeys.Operation {
	op := apikeys.Operation{
		Name:       database.OperationUpdateEvent,
		CalendarID: intent.CalendarID,
		Attendees:  intent.Attendees,
	}

	// Reading the existing event only matters when a constraint depends on it.
	if authKey.Constraints == nil {
		op.TimesKnown = intent.Start != nil && intent.End != nil
		if op.TimesKnown {
			op.Start, op.End = *intent.Start, *intent.End
		}
		return op
	}

	existing, err := h.calendarClient.GetEvent(ctx, intent.CalendarID, intent.EventID)
	if err != nil || existing == nil {
		util.Debug("Could not read the event being updated; requiring approval",
			"event_id", intent.EventID, "error", err)
		return op
	}

	op.Start = existing.Start.Time()
	op.End = existing.End.Time()
	op.AllDay = existing.Start.IsAllDay()

	if intent.Start != nil {
		op.Start = *intent.Start
		op.AllDay = false
	}
	if intent.End != nil {
		op.End = *intent.End
	}
	if len(intent.Attendees) == 0 {
		op.Attendees = attendeeEmails(existing.Attendees)
	}
	op.TimesKnown = !op.Start.IsZero() && !op.End.IsZero()

	return op
}

// evaluate applies the key's policy, writing the rejection response itself.
// It returns whether approval is required and whether to continue.
func (h *Handler) evaluate(w http.ResponseWriter, authKey *apikeys.AuthenticatedKey, op apikeys.Operation) (approvalRequired, ok bool) {
	result, violation := apikeys.Evaluate(authKey, op)

	switch result {
	case apikeys.ConstraintDeny:
		if violation != nil {
			response.WriteConstraintViolation(w, violation.Constraint, violation.Message)
		} else {
			response.WriteForbidden(w, "Operation denied by policy")
		}
		return false, false
	case apikeys.ConstraintRequireApproval:
		return true, true
	default:
		return false, true
	}
}

// calendarPermitted enforces the calendar allowlist for read operations.
func (h *Handler) calendarPermitted(w http.ResponseWriter, authKey *apikeys.AuthenticatedKey, calendarID string) bool {
	if calendarID == "" {
		response.WriteValidationError(w, "calendarId is required")
		return false
	}
	if err := util.ValidateCalendarID(calendarID); err != nil {
		response.WriteValidationError(w, err.Error())
		return false
	}
	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 &&
		!apikeys.CalendarAllowed(calendarID, authKey.Constraints.CalendarAllowlist) {
		response.WriteConstraintViolation(w, "calendar_allowlist", "Calendar is not in the allowed list")
		return false
	}
	return true
}

func attendeeEmails(attendees []google.Attendee) []string {
	if len(attendees) == 0 {
		return nil
	}
	emails := make([]string, 0, len(attendees))
	for _, a := range attendees {
		if a.Email != "" {
			emails = append(emails, a.Email)
		}
	}
	return emails
}

func splitAndTrim(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
