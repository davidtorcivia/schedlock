// Package response provides standardized HTTP response helpers.
package response

import (
	"encoding/json"
	"net/http"
)

// JSON writes a JSON response.
func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// Error writes a JSON error response.
func Error(w http.ResponseWriter, status int, message string, err error) {
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    statusToErrorCode(status),
			"message": message,
		},
	}

	if err != nil {
		resp["error"].(map[string]interface{})["details"] = map[string]string{
			"error": err.Error(),
		}
	}

	JSON(w, status, resp)
}

// ErrorWithCode writes a JSON error response with a specific error code.
func ErrorWithCode(w http.ResponseWriter, status int, code, message string, details map[string]interface{}) {
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}

	if details != nil {
		resp["error"].(map[string]interface{})["details"] = details
	}

	JSON(w, status, resp)
}

// statusToErrorCode maps HTTP status codes to error codes.
func statusToErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "VALIDATION_ERROR"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusInternalServerError:
		return "INTERNAL_ERROR"
	case http.StatusBadGateway:
		return "GOOGLE_API_ERROR"
	default:
		return "ERROR"
	}
}

// Error codes as defined in the design document.
const (
	ErrCodeInvalidAPIKey           = "INVALID_API_KEY"
	ErrCodeInsufficientPermissions = "INSUFFICIENT_PERMISSIONS"
	ErrCodeRateLimited             = "RATE_LIMITED"
	ErrCodeApprovalDenied          = "APPROVAL_DENIED"
	ErrCodeChangeRequested         = "CHANGE_REQUESTED"
	ErrCodeApprovalExpired         = "APPROVAL_EXPIRED"
	ErrCodeRequestNotFound         = "REQUEST_NOT_FOUND"
	ErrCodeGoogleAPIError          = "GOOGLE_API_ERROR"
	ErrCodeValidationError         = "VALIDATION_ERROR"
	ErrCodeNotCompleted            = "NOT_COMPLETED"
	ErrCodeAlreadyResolved         = "ALREADY_RESOLVED"
	ErrCodeRequestExpired          = "REQUEST_EXPIRED"
	ErrCodeConstraintViolation     = "CONSTRAINT_VIOLATION"
	ErrCodeUnauthorized            = "UNAUTHORIZED"
	ErrCodeInvalidToken            = "INVALID_TOKEN"
	ErrCodeTokenExpired            = "TOKEN_EXPIRED"
	ErrCodeTokenConsumed           = "TOKEN_CONSUMED"
	ErrCodeInternalError           = "INTERNAL_ERROR"
	ErrCodeNotImplemented          = "NOT_IMPLEMENTED"
)

// APIError represents a structured API error response.
type APIError struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	RequestID string                 `json:"requestId,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// ErrorResponse wraps an APIError in the standard response format.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteErrorWithDetails(w, status, code, message, "", nil)
}

// WriteErrorWithDetails writes a JSON error response with additional details.
func WriteErrorWithDetails(w http.ResponseWriter, status int, code, message, requestID string, details map[string]interface{}) {
	resp := ErrorResponse{
		Error: APIError{
			Code:      code,
			Message:   message,
			RequestID: requestID,
			Details:   details,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// Common error response helpers

// WriteInvalidAPIKey writes a 401 invalid API key error.
func WriteInvalidAPIKey(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, ErrCodeInvalidAPIKey, "API key missing or invalid")
}

// WriteInsufficientPermissions writes a 403 insufficient permissions error.
func WriteInsufficientPermissions(w http.ResponseWriter, tier, operation string) {
	WriteErrorWithDetails(w, http.StatusForbidden, ErrCodeInsufficientPermissions,
		"Operation not allowed for this API key tier",
		"", map[string]interface{}{
			"tier":      tier,
			"operation": operation,
		})
}

// WriteRateLimited writes a 429 rate limited error.
func WriteRateLimited(w http.ResponseWriter, retryAfter int) {
	w.Header().Set("Retry-After", string(rune(retryAfter)))
	WriteErrorWithDetails(w, http.StatusTooManyRequests, ErrCodeRateLimited,
		"Too many requests, please slow down",
		"", map[string]interface{}{
			"retry_after_seconds": retryAfter,
		})
}

// WriteValidationError writes a 400 validation error.
func WriteValidationError(w http.ResponseWriter, message string, details map[string]interface{}) {
	WriteErrorWithDetails(w, http.StatusBadRequest, ErrCodeValidationError, message, "", details)
}

// WriteRequestNotFound writes a 404 request not found error.
func WriteRequestNotFound(w http.ResponseWriter, requestID string) {
	WriteErrorWithDetails(w, http.StatusNotFound, ErrCodeRequestNotFound,
		"Request not found", requestID, nil)
}

// WriteApprovalDenied writes a 403 approval denied error.
func WriteApprovalDenied(w http.ResponseWriter, requestID string) {
	WriteErrorWithDetails(w, http.StatusForbidden, ErrCodeApprovalDenied,
		"Request was denied by approver", requestID, nil)
}

// WriteApprovalExpired writes a 408 approval expired error.
func WriteApprovalExpired(w http.ResponseWriter, requestID string) {
	WriteErrorWithDetails(w, http.StatusRequestTimeout, ErrCodeApprovalExpired,
		"Approval timeout reached", requestID, nil)
}

// WriteNotCompleted writes a 409 not completed error.
func WriteNotCompleted(w http.ResponseWriter, requestID, currentStatus string) {
	WriteErrorWithDetails(w, http.StatusConflict, ErrCodeNotCompleted,
		"Request has not completed yet", requestID,
		map[string]interface{}{"status": currentStatus})
}

// WriteAlreadyResolved writes a 409 already resolved error.
func WriteAlreadyResolved(w http.ResponseWriter, requestID, currentStatus string) {
	WriteErrorWithDetails(w, http.StatusConflict, ErrCodeAlreadyResolved,
		"Request has already been resolved", requestID,
		map[string]interface{}{"status": currentStatus})
}

// WriteGoogleAPIError writes a 502 Google API error.
func WriteGoogleAPIError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadGateway, ErrCodeGoogleAPIError, message)
}

// WriteInternalError writes a 500 internal error.
func WriteInternalError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, ErrCodeInternalError, message)
}

// WriteConstraintViolation writes a 403 constraint violation error.
func WriteConstraintViolation(w http.ResponseWriter, constraint, message string) {
	WriteErrorWithDetails(w, http.StatusForbidden, ErrCodeConstraintViolation,
		message, "", map[string]interface{}{"constraint": constraint})
}

// WriteInvalidToken writes a 401 invalid token error.
func WriteInvalidToken(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, ErrCodeInvalidToken, "Invalid or missing decision token")
}

// WriteTokenExpired writes a 401 token expired error.
func WriteTokenExpired(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, ErrCodeTokenExpired, "Decision token has expired")
}

// WriteTokenConsumed writes a 409 token already consumed error.
func WriteTokenConsumed(w http.ResponseWriter, action string) {
	WriteErrorWithDetails(w, http.StatusConflict, ErrCodeTokenConsumed,
		"This decision token has already been used", "",
		map[string]interface{}{"previous_action": action})
}

// WriteUnauthorized writes a 401 unauthorized error for web UI.
func WriteUnauthorized(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required")
}
