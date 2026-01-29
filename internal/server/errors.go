// Package server re-exports response helpers for backwards compatibility.
package server

import (
	"github.com/dtorcivia/schedlock/internal/response"
)

// Re-export error codes for backwards compatibility
const (
	ErrCodeInvalidAPIKey           = response.ErrCodeInvalidAPIKey
	ErrCodeInsufficientPermissions = response.ErrCodeInsufficientPermissions
	ErrCodeRateLimited             = response.ErrCodeRateLimited
	ErrCodeApprovalDenied          = response.ErrCodeApprovalDenied
	ErrCodeChangeRequested         = response.ErrCodeChangeRequested
	ErrCodeApprovalExpired         = response.ErrCodeApprovalExpired
	ErrCodeRequestNotFound         = response.ErrCodeRequestNotFound
	ErrCodeGoogleAPIError          = response.ErrCodeGoogleAPIError
	ErrCodeValidationError         = response.ErrCodeValidationError
	ErrCodeNotCompleted            = response.ErrCodeNotCompleted
	ErrCodeAlreadyResolved         = response.ErrCodeAlreadyResolved
	ErrCodeRequestExpired          = response.ErrCodeRequestExpired
	ErrCodeConstraintViolation     = response.ErrCodeConstraintViolation
	ErrCodeUnauthorized            = response.ErrCodeUnauthorized
	ErrCodeInvalidToken            = response.ErrCodeInvalidToken
	ErrCodeTokenExpired            = response.ErrCodeTokenExpired
	ErrCodeTokenConsumed           = response.ErrCodeTokenConsumed
	ErrCodeInternalError           = response.ErrCodeInternalError
	ErrCodeNotImplemented          = response.ErrCodeNotImplemented
)

// Re-export types
type APIError = response.APIError
type ErrorResponse = response.ErrorResponse

// Re-export functions
var (
	WriteError               = response.WriteError
	WriteErrorWithDetails    = response.WriteErrorWithDetails
	WriteInvalidAPIKey       = response.WriteInvalidAPIKey
	WriteInsufficientPermissions = response.WriteInsufficientPermissions
	WriteRateLimited         = response.WriteRateLimited
	WriteValidationError     = response.WriteValidationError
	WriteRequestNotFound     = response.WriteRequestNotFound
	WriteApprovalDenied      = response.WriteApprovalDenied
	WriteApprovalExpired     = response.WriteApprovalExpired
	WriteNotCompleted        = response.WriteNotCompleted
	WriteAlreadyResolved     = response.WriteAlreadyResolved
	WriteGoogleAPIError      = response.WriteGoogleAPIError
	WriteInternalError       = response.WriteInternalError
	WriteConstraintViolation = response.WriteConstraintViolation
	WriteInvalidToken        = response.WriteInvalidToken
	WriteTokenExpired        = response.WriteTokenExpired
	WriteTokenConsumed       = response.WriteTokenConsumed
	WriteUnauthorized        = response.WriteUnauthorized
	JSON                     = response.JSON
	Error                    = response.Error
	ErrorWithCode            = response.ErrorWithCode
)
