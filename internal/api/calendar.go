package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/util"
)

// ListCalendars returns all accessible calendars.
func (h *Handler) ListCalendars(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	ctx := r.Context()
	calendars, err := h.calendarClient.ListCalendars(ctx)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to list calendars", err)
		return
	}

	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		calendars = filterCalendars(calendars, authKey.Constraints.CalendarAllowlist)
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"calendars": calendars,
	})
}

// ListEvents returns events from a calendar.
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	calendarID := r.PathValue("calendarId")
	if calendarID == "" {
		response.Error(w, http.StatusBadRequest, "calendar ID required", nil)
		return
	}

	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		if !calendarAllowed(calendarID, authKey.Constraints.CalendarAllowlist) {
			response.WriteConstraintViolation(w, "calendar_allowlist", "calendar not in allowlist")
			return
		}
	}

	// Parse query parameters
	var timeMin, timeMax time.Time
	var err error

	if minStr := r.URL.Query().Get("timeMin"); minStr != "" {
		timeMin, err = time.Parse(time.RFC3339, minStr)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid timeMin format (use RFC3339)", nil)
			return
		}
	} else {
		timeMin = time.Now()
	}

	if maxStr := r.URL.Query().Get("timeMax"); maxStr != "" {
		timeMax, err = time.Parse(time.RFC3339, maxStr)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid timeMax format (use RFC3339)", nil)
			return
		}
	} else {
		timeMax = timeMin.AddDate(0, 1, 0) // Default to 1 month
	}

	maxResults := 50
	if maxStr := r.URL.Query().Get("maxResults"); maxStr != "" {
		if n, err := strconv.Atoi(maxStr); err == nil && n > 0 && n <= 250 {
			maxResults = n
		}
	}
	pageToken := r.URL.Query().Get("pageToken")
	queryText := r.URL.Query().Get("q")
	singleEvents := true
	if singleStr := r.URL.Query().Get("singleEvents"); singleStr != "" {
		if singleEvents, err = strconv.ParseBool(singleStr); err != nil {
			response.Error(w, http.StatusBadRequest, "invalid singleEvents value", nil)
			return
		}
	}
	orderBy := r.URL.Query().Get("orderBy")

	ctx := r.Context()
	eventsResp, err := h.calendarClient.ListEvents(ctx, google.EventListOptions{
		CalendarID:   calendarID,
		TimeMin:      timeMin,
		TimeMax:      timeMax,
		MaxResults:   maxResults,
		PageToken:    pageToken,
		Query:        queryText,
		SingleEvents: singleEvents,
		OrderBy:      orderBy,
	})
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to list events", err)
		return
	}

	resp := map[string]interface{}{
		"events": eventsResp.Events,
	}
	if eventsResp.NextPageToken != "" {
		resp["next_page_token"] = eventsResp.NextPageToken
	}
	response.JSON(w, http.StatusOK, resp)
}

// GetEvent returns a single event.
func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	calendarID := r.PathValue("calendarId")
	eventID := r.PathValue("eventId")

	if calendarID == "" || eventID == "" {
		response.Error(w, http.StatusBadRequest, "calendar ID and event ID required", nil)
		return
	}

	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		if !calendarAllowed(calendarID, authKey.Constraints.CalendarAllowlist) {
			response.WriteConstraintViolation(w, "calendar_allowlist", "calendar not in allowlist")
			return
		}
	}

	ctx := r.Context()
	event, err := h.calendarClient.GetEvent(ctx, calendarID, eventID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get event", err)
		return
	}

	if event == nil {
		response.Error(w, http.StatusNotFound, "event not found", nil)
		return
	}

	response.JSON(w, http.StatusOK, event)
}

// FreeBusyRequest represents a free/busy query.
type FreeBusyRequest struct {
	TimeMin    time.Time `json:"timeMin"`
	TimeMax    time.Time `json:"timeMax"`
	Calendars  []string  `json:"calendars"`
	TimeMinAlt time.Time `json:"time_min"`
	TimeMaxAlt time.Time `json:"time_max"`
}

// FreeBusy returns free/busy information.
func (h *Handler) FreeBusy(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	var req FreeBusyRequest
	if err := parseJSON(r, &req); err != nil {
		// Try query parameters as fallback
		var parseErr error
		req.TimeMin, parseErr = time.Parse(time.RFC3339, r.URL.Query().Get("timeMin"))
		if parseErr != nil {
			req.TimeMin = time.Now()
		}
		req.TimeMax, parseErr = time.Parse(time.RFC3339, r.URL.Query().Get("timeMax"))
		if parseErr != nil {
			req.TimeMax = req.TimeMin.AddDate(0, 0, 7) // Default 1 week
		}
		if cals := r.URL.Query().Get("calendars"); cals != "" {
			req.Calendars = []string{cals}
		}
	}

	if len(req.Calendars) == 0 {
		req.Calendars = []string{"primary"}
	}
	if req.TimeMin.IsZero() && !req.TimeMinAlt.IsZero() {
		req.TimeMin = req.TimeMinAlt
	}
	if req.TimeMax.IsZero() && !req.TimeMaxAlt.IsZero() {
		req.TimeMax = req.TimeMaxAlt
	}

	if authKey.Constraints != nil && len(authKey.Constraints.CalendarAllowlist) > 0 {
		var filtered []string
		for _, cal := range req.Calendars {
			if calendarAllowed(cal, authKey.Constraints.CalendarAllowlist) {
				filtered = append(filtered, cal)
			}
		}
		if len(filtered) == 0 {
			response.WriteConstraintViolation(w, "calendar_allowlist", "no calendars allowed for this key")
			return
		}
		req.Calendars = filtered
	}

	ctx := r.Context()
	// Build FreeBusy request
	fbReq := &google.FreeBusyRequest{
		TimeMin: req.TimeMin,
		TimeMax: req.TimeMax,
	}
	for _, cal := range req.Calendars {
		fbReq.Items = append(fbReq.Items, google.FreeBusyCalendar{ID: cal})
	}
	result, err := h.calendarClient.FreeBusy(ctx, fbReq)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get free/busy", err)
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// CreateEvent initiates a create event request (requires approval).
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "write")
	if authKey == nil {
		return
	}

	var intent google.EventIntent
	if err := parseJSON(r, &intent); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate intent
	if err := intent.Validate(); err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	intent.Sanitize()

	approvalRequired, err := h.evaluateConstraintsForCreate(authKey, &intent)
	if err != nil {
		writeConstraintError(w, err)
		return
	}

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationCreateEvent, payload, idempotencyKey, approvalRequired, "policy")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	statusCode := http.StatusAccepted
	if !approvalRequired {
		statusCode = http.StatusOK
	}
	response.JSON(w, statusCode, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event creation request submitted",
	})
}

// UpdateEvent initiates an update event request (requires approval).
func (h *Handler) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "write")
	if authKey == nil {
		return
	}

	var intent google.EventUpdateIntent
	if err := parseJSON(r, &intent); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate intent
	if err := intent.Validate(); err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !intent.HasChanges() {
		response.Error(w, http.StatusBadRequest, "no changes provided", nil)
		return
	}
	sanitizeUpdateIntent(&intent)

	approvalRequired, err := h.evaluateConstraintsForUpdate(r.Context(), authKey, &intent)
	if err != nil {
		writeConstraintError(w, err)
		return
	}

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationUpdateEvent, payload, idempotencyKey, approvalRequired, "policy")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	statusCode := http.StatusAccepted
	if !approvalRequired {
		statusCode = http.StatusOK
	}
	response.JSON(w, statusCode, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event update request submitted",
	})
}

// DeleteEvent initiates a delete event request (requires approval).
func (h *Handler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "write")
	if authKey == nil {
		return
	}

	var intent google.EventDeleteIntent
	if err := parseJSON(r, &intent); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate intent
	if err := intent.Validate(); err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	approvalRequired, err := h.evaluateConstraintsForDelete(authKey, &intent)
	if err != nil {
		writeConstraintError(w, err)
		return
	}

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationDeleteEvent, payload, idempotencyKey, approvalRequired, "policy")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	statusCode := http.StatusAccepted
	if !approvalRequired {
		statusCode = http.StatusOK
	}
	response.JSON(w, statusCode, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event deletion request submitted",
	})
}

// Helpers

func (h *Handler) evaluateConstraintsForCreate(authKey *apikeys.AuthenticatedKey, intent *google.EventIntent) (bool, error) {
	result, violation := apikeys.EvaluateConstraints(
		authKey,
		database.OperationCreateEvent,
		intent.CalendarID,
		intent.Attendees,
		intent.Start,
		intent.End,
	)
	return handleConstraintResult(result, violation)
}

func (h *Handler) evaluateConstraintsForUpdate(ctx context.Context, authKey *apikeys.AuthenticatedKey, intent *google.EventUpdateIntent) (bool, error) {
	// If no constraints, rely on tier defaults only.
	if authKey.Constraints == nil {
		result, violation := apikeys.EvaluateConstraints(
			authKey,
			database.OperationUpdateEvent,
			intent.CalendarID,
			intent.Attendees,
			time.Now(),
			time.Now(),
		)
		return handleConstraintResult(result, violation)
	}

	// Fetch existing event to compute effective values
	existing, err := h.calendarClient.GetEvent(ctx, intent.CalendarID, intent.EventID)
	if err != nil || existing == nil {
		// Fail closed: require approval if we cannot evaluate safely
		return true, nil
	}

	start := extractEventTime(existing.Start)
	end := extractEventTime(existing.End)
	attendees := extractAttendees(existing.Attendees)

	if intent.Start != nil {
		start = *intent.Start
	}
	if intent.End != nil {
		end = *intent.End
	}
	if len(intent.Attendees) > 0 {
		attendees = intent.Attendees
	}

	if !start.IsZero() && !end.IsZero() {
		if err := util.ValidateTimeRange(start, end, false); err != nil {
			return false, err
		}
	}

	result, violation := apikeys.EvaluateConstraints(
		authKey,
		database.OperationUpdateEvent,
		intent.CalendarID,
		attendees,
		start,
		end,
	)
	return handleConstraintResult(result, violation)
}

func (h *Handler) evaluateConstraintsForDelete(authKey *apikeys.AuthenticatedKey, intent *google.EventDeleteIntent) (bool, error) {
	now := time.Now()
	result, violation := apikeys.EvaluateConstraints(
		authKey,
		database.OperationDeleteEvent,
		intent.CalendarID,
		nil,
		now,
		now,
	)
	return handleConstraintResult(result, violation)
}

func handleConstraintResult(result apikeys.ConstraintResult, violation *apikeys.ConstraintViolation) (bool, error) {
	switch result {
	case apikeys.ConstraintDeny:
		if violation != nil {
			return false, violation
		}
		return false, fmt.Errorf("operation denied by policy")
	case apikeys.ConstraintRequireApproval:
		return true, nil
	default:
		return false, nil
	}
}

func writeConstraintError(w http.ResponseWriter, err error) {
	if errors.Is(err, util.ErrPastTime) || errors.Is(err, util.ErrEndBeforeStart) {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if violation, ok := err.(*apikeys.ConstraintViolation); ok {
		response.WriteConstraintViolation(w, violation.Constraint, violation.Message)
		return
	}
	response.Error(w, http.StatusForbidden, err.Error(), nil)
}

func calendarAllowed(calendarID string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if allowed == "*" || allowed == calendarID {
			return true
		}
	}
	return false
}

func filterCalendars(calendars []google.Calendar, allowlist []string) []google.Calendar {
	var filtered []google.Calendar
	for _, cal := range calendars {
		if calendarAllowed(cal.ID, allowlist) {
			filtered = append(filtered, cal)
		}
	}
	return filtered
}

func extractEventTime(eventTime *google.EventTime) time.Time {
	if eventTime == nil {
		return time.Time{}
	}
	if !eventTime.DateTime.IsZero() {
		return eventTime.DateTime
	}
	if eventTime.Date != "" {
		if t, err := time.Parse("2006-01-02", eventTime.Date); err == nil {
			return t
		}
	}
	return time.Time{}
}

func extractAttendees(attendees []google.Attendee) []string {
	if len(attendees) == 0 {
		return nil
	}
	result := make([]string, 0, len(attendees))
	for _, attendee := range attendees {
		if attendee.Email != "" {
			result = append(result, attendee.Email)
		}
	}
	return result
}

func sanitizeUpdateIntent(intent *google.EventUpdateIntent) {
	if intent.Summary != nil {
		v := util.SanitizeString(*intent.Summary)
		intent.Summary = &v
	}
	if intent.Description != nil {
		v := util.SanitizeString(*intent.Description)
		intent.Description = &v
	}
	if intent.Location != nil {
		v := util.SanitizeString(*intent.Location)
		intent.Location = &v
	}
}
