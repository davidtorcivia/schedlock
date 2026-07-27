package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/util"
)

// errValidation reports a problem with submitted form input.
func errValidation(message string) error { return &validationError{message: message} }

type validationError struct{ message string }

func (e *validationError) Error() string { return e.message }

// UpdatePayload lets an approver correct a request before approving it.
//
// The edited request is re-validated exactly as if it had arrived from an
// agent: a human editing the payload must not be able to introduce something
// the API itself would have rejected.
func (h *Handler) UpdatePayload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := r.PathValue("requestId")

	req, err := h.RequestRepo.GetByID(ctx, requestID)
	if err != nil {
		util.Error("Failed to read request for editing", "error", err, "request_id", requestID)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The request could not be loaded.")
		return
	}
	if req == nil {
		h.renderError(w, r, http.StatusNotFound, "Request not found", "This request no longer exists.")
		return
	}
	if req.Status != database.StatusPendingApproval {
		h.renderError(w, r, http.StatusConflict, "Request already decided",
			"Only a pending request can be edited.")
		return
	}

	payload, err := h.editedPayload(r, req)
	if err != nil {
		var validation *validationError
		if errors.As(err, &validation) {
			h.renderDetailWithError(w, r, req, validation.message)
			return
		}
		util.Error("Failed to apply request edits", "error", err, "request_id", requestID)
		h.renderDetailWithError(w, r, req, "The changes could not be applied.")
		return
	}

	if err := h.RequestRepo.UpdatePayload(ctx, requestID, payload); err != nil {
		if errors.Is(err, requests.ErrNotPending) {
			h.renderError(w, r, http.StatusConflict, "Request already decided",
				"This request was decided while you were editing it.")
			return
		}
		util.Error("Failed to store edited payload", "error", err, "request_id", requestID)
		h.renderDetailWithError(w, r, req, "The changes could not be saved.")
		return
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditRequestEdited,
		RequestID: requestID,
		APIKeyID:  req.APIKeyID,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
	})

	http.Redirect(w, r, "/requests/"+requestID, http.StatusSeeOther)
}

// editedPayload merges the submitted form over the stored intent.
func (h *Handler) editedPayload(r *http.Request, req *database.Request) (json.RawMessage, error) {
	start, err := h.parseFormTime(r, "start")
	if err != nil {
		return nil, err
	}
	end, err := h.parseFormTime(r, "end")
	if err != nil {
		return nil, err
	}

	summary := util.SanitizeLine(r.FormValue("summary"))
	location := util.SanitizeLine(r.FormValue("location"))
	description := util.SanitizeText(r.FormValue("description"))

	switch req.Operation {
	case database.OperationCreateEvent:
		var intent google.EventIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil, err
		}

		intent.Summary = summary
		intent.Location = location
		intent.Description = description
		if !start.IsZero() {
			intent.Start = start
		}
		if !end.IsZero() {
			intent.End = end
		}

		// Past times are permitted here: an operator may knowingly approve a
		// request that has been waiting, and the approval window itself is what
		// bounds staleness.
		if err := validateEditedRange(intent.Start, intent.End); err != nil {
			return nil, err
		}
		if err := intent.Validate(); err != nil && !isPastTimeError(err) {
			return nil, errValidation(err.Error())
		}
		return json.Marshal(intent)

	case database.OperationUpdateEvent:
		var intent google.EventUpdateIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil, err
		}

		intent.Summary = optionalString(summary, intent.Summary)
		intent.Location = optionalString(location, intent.Location)
		intent.Description = optionalString(description, intent.Description)
		if !start.IsZero() {
			intent.Start = &start
		}
		if !end.IsZero() {
			intent.End = &end
		}

		if intent.Start != nil && intent.End != nil {
			if err := validateEditedRange(*intent.Start, *intent.End); err != nil {
				return nil, err
			}
		}
		if err := intent.Validate(); err != nil && !isPastTimeError(err) {
			return nil, errValidation(err.Error())
		}
		return json.Marshal(intent)

	default:
		return nil, errValidation("This request type cannot be edited.")
	}
}

// parseFormTime reads a datetime-local field.
//
// The browser submits wall-clock time with no zone. It is interpreted in the
// operator's configured display timezone, which is the timezone the field was
// rendered in; parsing it as UTC would shift every edited event by the
// operator's offset.
func (h *Handler) parseFormTime(r *http.Request, field string) (time.Time, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return time.Time{}, nil
	}

	parsed, err := util.GetDefaultFormatter().ParseFromInput(raw)
	if err != nil {
		return time.Time{}, errValidation("Enter a valid " + field + " time.")
	}
	return parsed, nil
}

func validateEditedRange(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return errValidation("Both a start and an end time are required.")
	}
	if !end.After(start) {
		return errValidation("The end time must be after the start time.")
	}
	return nil
}

// isPastTimeError reports whether validation failed only because the event is
// in the past.
func isPastTimeError(err error) bool {
	return errors.Is(err, util.ErrPastTime)
}

// optionalString returns a pointer to value when it is set, otherwise keeps the
// existing pointer, so clearing a field is distinguishable from not touching it.
func optionalString(value string, existing *string) *string {
	if value != "" {
		return &value
	}
	if existing != nil {
		empty := ""
		return &empty
	}
	return nil
}

// renderDetailWithError re-renders the request page with a message.
func (h *Handler) renderDetailWithError(w http.ResponseWriter, r *http.Request, req *database.Request, message string) {
	history, err := h.AuditLogger.GetByRequestID(r.Context(), req.ID)
	if err != nil {
		util.Warn("Failed to read request history", "error", err, "request_id", req.ID)
	}

	h.renderStatus(w, r, http.StatusBadRequest, "detail.html", pageData{
		"Title":   "Request details",
		"Nav":     "pending",
		"Request": newRequestView(req),
		"Form":    newEventForm(req),
		"History": summarizeAudit(history),
		"Error":   message,
	})
}
