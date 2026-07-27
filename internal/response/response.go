// Package response provides the JSON envelope shared by every API endpoint.
package response

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/dtorcivia/schedlock/internal/util"
)

// Error codes returned to API clients.
const (
	CodeInvalidAPIKey           = "INVALID_API_KEY"
	CodeInsufficientPermissions = "INSUFFICIENT_PERMISSIONS"
	CodeRateLimited             = "RATE_LIMITED"
	CodeRequestNotFound         = "REQUEST_NOT_FOUND"
	CodeUpstreamError           = "GOOGLE_API_ERROR"
	CodeValidationError         = "VALIDATION_ERROR"
	CodeConflict                = "CONFLICT"
	CodeConstraintViolation     = "CONSTRAINT_VIOLATION"
	CodeUnauthorized            = "UNAUTHORIZED"
	CodeInvalidToken            = "INVALID_TOKEN"
	CodeInternalError           = "INTERNAL_ERROR"
	CodeForbidden               = "FORBIDDEN"
)

// APIError is the error body returned to clients.
type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// ErrorResponse wraps an APIError in the standard envelope.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// The status line is already committed, so the response cannot be
		// rewritten; record the failure for operators instead.
		util.Error("Failed to encode JSON response", "error", err)
	}
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteErrorWithDetails(w, status, code, message, "", nil)
}

// WriteErrorWithDetails writes a JSON error response with structured details.
//
// Details are for the caller's benefit (which constraint failed, which tier is
// required). Internal failure text never belongs here: it is logged instead, so
// database and upstream errors are not handed to API clients.
func WriteErrorWithDetails(w http.ResponseWriter, status int, code, message, requestID string, details map[string]any) {
	JSON(w, status, ErrorResponse{
		Error: APIError{
			Code:      code,
			Message:   message,
			RequestID: requestID,
			Details:   details,
		},
	})
}

// WriteInternalError logs the underlying cause and returns an opaque 500.
func WriteInternalError(w http.ResponseWriter, publicMessage string, cause error, logFields ...any) {
	if cause != nil {
		util.Error(publicMessage, append([]any{"error", cause}, logFields...)...)
	}
	if publicMessage == "" {
		publicMessage = "An unexpected error occurred"
	}
	WriteError(w, http.StatusInternalServerError, CodeInternalError, publicMessage)
}

// WriteUpstreamError reports a failure calling Google, logging the detail.
func WriteUpstreamError(w http.ResponseWriter, publicMessage string, cause error) {
	if cause != nil {
		util.Error(publicMessage, "error", cause)
	}
	WriteError(w, http.StatusBadGateway, CodeUpstreamError, publicMessage)
}

// WriteInvalidAPIKey writes a 401 for a missing or unusable API key.
func WriteInvalidAPIKey(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, CodeInvalidAPIKey, "API key missing or invalid")
}

// WriteInsufficientPermissions writes a 403 for a tier that cannot perform the
// requested operation.
func WriteInsufficientPermissions(w http.ResponseWriter, tier, operation string) {
	WriteErrorWithDetails(w, http.StatusForbidden, CodeInsufficientPermissions,
		"Operation not allowed for this API key tier", "",
		map[string]any{"tier": tier, "operation": operation})
}

// WriteRateLimited writes a 429 with a Retry-After hint.
func WriteRateLimited(w http.ResponseWriter, retryAfterSeconds int) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	WriteErrorWithDetails(w, http.StatusTooManyRequests, CodeRateLimited,
		"Too many requests, please slow down", "",
		map[string]any{"retry_after_seconds": retryAfterSeconds})
}

// WriteValidationError writes a 400 for malformed or rejected input.
func WriteValidationError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, CodeValidationError, message)
}

// WriteRequestNotFound writes a 404 for an unknown request ID.
func WriteRequestNotFound(w http.ResponseWriter, requestID string) {
	WriteErrorWithDetails(w, http.StatusNotFound, CodeRequestNotFound, "Request not found", requestID, nil)
}

// WriteConflict writes a 409 for an operation that no longer applies.
func WriteConflict(w http.ResponseWriter, message, requestID string, details map[string]any) {
	WriteErrorWithDetails(w, http.StatusConflict, CodeConflict, message, requestID, details)
}

// WriteForbidden writes a 403.
func WriteForbidden(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusForbidden, CodeForbidden, message)
}

// WriteConstraintViolation writes a 403 naming the policy constraint that
// rejected the operation.
func WriteConstraintViolation(w http.ResponseWriter, constraint, message string) {
	WriteErrorWithDetails(w, http.StatusForbidden, CodeConstraintViolation, message, "",
		map[string]any{"constraint": constraint})
}

// WriteInvalidToken writes a 400 for a decision token that cannot be used.
func WriteInvalidToken(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, CodeInvalidToken, message)
}
