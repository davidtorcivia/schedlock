package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/response"
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

	ctx := r.Context()
	eventsResp, err := h.calendarClient.ListEvents(ctx, google.EventListOptions{
		CalendarID:   calendarID,
		TimeMin:      timeMin,
		TimeMax:      timeMax,
		MaxResults:   maxResults,
		SingleEvents: true,
		OrderBy:      "startTime",
	})
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to list events", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"events": eventsResp.Events,
	})
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
	TimeMin    time.Time `json:"time_min"`
	TimeMax    time.Time `json:"time_max"`
	Calendars  []string  `json:"calendars"`
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

	// Check constraints
	if authKey.Constraints != nil {
		if err := checkConstraints(authKey.Constraints, &intent); err != nil {
			response.Error(w, http.StatusForbidden, err.Error(), nil)
			return
		}
	}

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationCreateEvent, payload, idempotencyKey)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event creation request submitted for approval",
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

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationUpdateEvent, payload, idempotencyKey)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event update request submitted for approval",
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

	// Get idempotency key
	idempotencyKey := r.Header.Get("Idempotency-Key")

	// Marshal payload
	payload, _ := json.Marshal(intent)

	// Submit request
	ctx := r.Context()
	req, err := h.engine.SubmitRequest(ctx, authKey, database.OperationDeleteEvent, payload, idempotencyKey)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to submit request", err)
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]interface{}{
		"request_id": req.ID,
		"status":     req.Status,
		"expires_at": req.ExpiresAt,
		"message":    "Event deletion request submitted for approval",
	})
}

// checkConstraints validates intent against API key constraints.
func checkConstraints(constraints *database.KeyConstraints, intent *google.EventIntent) error {
	// Check calendar ID allowlist
	if len(constraints.CalendarAllowlist) > 0 {
		allowed := false
		for _, cal := range constraints.CalendarAllowlist {
			if cal == intent.CalendarID || cal == "*" {
				allowed = true
				break
			}
		}
		if !allowed {
			return &constraintError{"calendar not in allowlist"}
		}
	}

	// Check max duration
	if constraints.MaxDurationMinutes > 0 && intent.End.Sub(intent.Start).Minutes() > float64(constraints.MaxDurationMinutes) {
		return &constraintError{"event duration exceeds maximum"}
	}

	// Check max attendees
	if constraints.MaxAttendees > 0 && len(intent.Attendees) > constraints.MaxAttendees {
		return &constraintError{"too many attendees"}
	}

	return nil
}

type constraintError struct {
	message string
}

func (e *constraintError) Error() string {
	return e.message
}
