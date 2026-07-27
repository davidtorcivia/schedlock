package web

import (
	"errors"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// PublicApprove serves the token-authenticated approval page.
//
// GET renders what is being asked for and waits; POST records the decision.
// Keeping the decision on POST is deliberate: link previewers and mail scanners
// routinely fetch URLs they are merely shown, and a GET that approved would let
// them decide on the operator's behalf.
func (h *Handler) PublicApprove(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		h.renderApproveError(w, r, "Invalid link", "This approval link is missing its token.")
		return
	}

	if r.Method == http.MethodPost {
		h.submitDecision(w, r, token)
		return
	}

	h.showApprovalPage(w, r, token, "")
}

// showApprovalPage renders the pending request behind a token.
func (h *Handler) showApprovalPage(w http.ResponseWriter, r *http.Request, token, pinError string) {
	ctx := r.Context()

	result, err := h.TokenRepo.Validate(ctx, token)
	if err != nil {
		h.renderTokenError(w, r, err)
		return
	}

	req, err := h.RequestRepo.GetByID(ctx, result.RequestID)
	if err != nil {
		util.Error("Failed to load request for approval page", "error", err, "request_id", result.RequestID)
		h.renderApproveError(w, r, "Something went wrong", "This request could not be loaded.")
		return
	}
	if req == nil {
		h.renderApproveError(w, r, "Request not found", "This request no longer exists.")
		return
	}

	if req.Status != database.StatusPendingApproval {
		h.renderApproveError(w, r, "Already decided",
			"This request has already been "+statusPhrase(req.Status)+".")
		return
	}

	requiresPIN, err := h.SettingsStore.HasApprovalPIN(ctx)
	if err != nil {
		util.Error("Failed to read approval PIN setting", "error", err)
	}

	view := newRequestView(req)

	status := http.StatusOK
	if pinError != "" {
		status = http.StatusUnauthorized
	}

	h.renderStatus(w, r, status, "approve.html", pageData{
		"Title":       "Approve request",
		"Token":       token,
		"Request":     view,
		"Details":     view.Details,
		"ExpiresIn":   util.GetDefaultFormatter().FormatExpiresIn(req.ExpiresAt),
		"RequiresPIN": requiresPIN,
		"PINError":    pinError,
	})
}

// submitDecision consumes the token and applies the decision.
func (h *Handler) submitDecision(w http.ResponseWriter, r *http.Request, token string) {
	ctx := r.Context()

	action := r.FormValue("action")
	if action != "approve" && action != "deny" {
		h.renderApproveError(w, r, "Invalid action", "Please use the approve or deny buttons.")
		return
	}

	if ok := h.checkApprovalPIN(w, r, token); !ok {
		return
	}

	// The token is consumed before the decision is applied, so a link cannot
	// be replayed even if the decision itself fails afterwards.
	requestID, err := h.TokenRepo.Consume(ctx, token, action)
	if err != nil {
		h.renderTokenError(w, r, err)
		return
	}

	if err := h.Engine.ProcessApproval(ctx, requestID, action, "approval_link"); err != nil {
		util.Warn("Approval link decision rejected", "error", err, "request_id", requestID)
		h.renderApproveError(w, r, "Could not record the decision", decisionErrorMessage(err))
		return
	}

	message := "The calendar change has been approved and is being applied."
	if action == "deny" {
		message = "The calendar change has been denied. Nothing was written to your calendar."
	}

	h.renderStatus(w, r, http.StatusOK, "approve.html", pageData{
		"Title":   "Decision recorded",
		"Success": true,
		"Action":  action,
		"Message": message,
	})
}

// checkApprovalPIN enforces the optional approval PIN.
func (h *Handler) checkApprovalPIN(w http.ResponseWriter, r *http.Request, token string) bool {
	ctx := r.Context()

	requiresPIN, err := h.SettingsStore.HasApprovalPIN(ctx)
	if err != nil {
		util.Error("Failed to read approval PIN setting", "error", err)
		h.renderApproveError(w, r, "Something went wrong", "The PIN could not be verified.")
		return false
	}
	if !requiresPIN {
		return true
	}

	// A PIN is short enough to guess quickly, so attempts are throttled per
	// client address in the same way as the admin password.
	clientIP := h.ClientIP(r)
	if !h.pinLimiter.Allow(clientIP) {
		util.Warn("Approval PIN rate limit reached", "client_ip", clientIP)
		h.showApprovalPage(w, r, token, "Too many attempts. Please wait a few minutes and try again.")
		return false
	}

	valid, err := h.SettingsStore.VerifyApprovalPIN(ctx, r.FormValue("pin"))
	if err != nil {
		util.Error("Failed to verify approval PIN", "error", err)
		h.renderApproveError(w, r, "Something went wrong", "The PIN could not be verified.")
		return false
	}
	if !valid {
		h.showApprovalPage(w, r, token, "Incorrect PIN.")
		return false
	}

	h.pinLimiter.Reset(clientIP)
	return true
}

// renderTokenError explains why an approval link cannot be used.
func (h *Handler) renderTokenError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tokens.ErrTokenConsumed):
		h.renderApproveError(w, r, "Link already used",
			"This approval link has already been used. Sign in to see the request's current state.")
	case errors.Is(err, tokens.ErrTokenExpired):
		h.renderApproveError(w, r, "Link expired",
			"This approval link has expired. The request may have timed out.")
	case errors.Is(err, tokens.ErrTokenNotFound):
		h.renderApproveError(w, r, "Invalid link",
			"This approval link is not valid. It may have been cleaned up after the request was resolved.")
	default:
		util.Error("Failed to validate approval token", "error", err)
		h.renderApproveError(w, r, "Something went wrong", "This approval link could not be checked.")
	}
}

// renderApproveError renders the approval page in its error state.
func (h *Handler) renderApproveError(w http.ResponseWriter, r *http.Request, title, message string) {
	h.renderStatus(w, r, http.StatusBadRequest, "approve.html", pageData{
		"Title":      title,
		"ErrorTitle": title,
		"Error":      message,
	})
}

// statusPhrase renders a status for use in a sentence.
func statusPhrase(status string) string {
	switch status {
	case database.StatusChangeRequested:
		return "sent back for changes"
	case database.StatusPendingApproval:
		return "left pending"
	default:
		return status
	}
}

// decisionErrorMessage renders an engine error for a non-technical reader.
func decisionErrorMessage(err error) string {
	var alreadyDecided *engine.ErrAlreadyDecided
	if errors.As(err, &alreadyDecided) {
		return "This request has already been " + statusPhrase(alreadyDecided.Status) + "."
	}
	return "The request could not be updated. It may have already been resolved."
}
