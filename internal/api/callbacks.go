package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// RegisterCallbackRoutes registers the decision callback endpoints.
//
// These authenticate with a single-use decision token in the path rather than
// an API key, so they are mounted outside the authenticated API surface.
func (h *Handler) RegisterCallbackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/callback/approve/{token}", h.ApproveCallback)
	mux.HandleFunc("POST /api/callback/deny/{token}", h.DenyCallback)
	mux.HandleFunc("POST /api/callback/suggest/{token}", h.SuggestCallback)

	// GET is answered with a redirect to the confirmation page rather than by
	// acting. See handleDecisionGET.
	mux.HandleFunc("GET /api/callback/approve/{token}", h.decisionGET)
	mux.HandleFunc("GET /api/callback/deny/{token}", h.decisionGET)
	mux.HandleFunc("GET /api/callback/suggest/{token}", h.decisionGET)
}

// ApproveCallback approves a request.
func (h *Handler) ApproveCallback(w http.ResponseWriter, r *http.Request) {
	h.handleDecision(w, r, tokens.ActionApprove)
}

// DenyCallback denies a request.
func (h *Handler) DenyCallback(w http.ResponseWriter, r *http.Request) {
	h.handleDecision(w, r, tokens.ActionDeny)
}

// decisionGET redirects a browser to the confirmation page.
//
// A decision must never be the side effect of a GET. Link previewers in chat
// clients, mail scanners, and browser prefetching all fetch URLs they are
// merely shown, and an approval link that acts on GET would let any of them
// silently approve a calendar change on the operator's behalf. The human-facing
// page at /approve/{token} asks for confirmation before anything happens.
func (h *Handler) decisionGET(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		response.WriteInvalidToken(w, "No approval token supplied")
		return
	}
	http.Redirect(w, r, "/approve/"+token, http.StatusSeeOther)
}

// handleDecision consumes a decision token and applies the decision.
func (h *Handler) handleDecision(w http.ResponseWriter, r *http.Request, action string) {
	token := r.PathValue("token")
	if token == "" {
		response.WriteInvalidToken(w, "No approval token supplied")
		return
	}

	ctx := r.Context()

	requestID, err := h.tokenRepo.Consume(ctx, token, action)
	if err != nil {
		writeTokenError(w, err)
		return
	}

	if err := h.engine.ProcessApproval(ctx, requestID, action, "callback"); err != nil {
		writeDecisionError(w, requestID, err)
		return
	}

	util.Info("Decision recorded from callback", "request_id", requestID, "action", action)

	response.JSON(w, http.StatusOK, map[string]any{
		"message":    "Request " + action + "d",
		"request_id": requestID,
		"action":     action,
	})
}

// SuggestCallback records requested changes against a pending request.
func (h *Handler) SuggestCallback(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		response.WriteInvalidToken(w, "No approval token supplied")
		return
	}

	var body struct {
		Suggestion string `json:"suggestion"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			response.WriteValidationError(w, "invalid request body: "+err.Error())
			return
		}
	}

	suggestion := strings.TrimSpace(body.Suggestion)
	if suggestion == "" {
		suggestion = strings.TrimSpace(r.URL.Query().Get("suggestion"))
	}
	if suggestion == "" {
		response.WriteValidationError(w, "suggestion text is required")
		return
	}

	ctx := r.Context()

	requestID, err := h.tokenRepo.Consume(ctx, token, tokens.ActionSuggest)
	if err != nil {
		writeTokenError(w, err)
		return
	}

	if err := h.engine.ProcessSuggestion(ctx, requestID, suggestion, "callback"); err != nil {
		writeDecisionError(w, requestID, err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"message":    "Suggestion recorded",
		"request_id": requestID,
	})
}

// writeTokenError maps a token failure onto an HTTP response.
//
// The sentinel errors carry text written for a person holding a stale link, and
// none of them reveal anything about the request behind the token.
func writeTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tokens.ErrTokenNotFound),
		errors.Is(err, tokens.ErrTokenExpired),
		errors.Is(err, tokens.ErrActionAllowed):
		response.WriteInvalidToken(w, err.Error())
	case errors.Is(err, tokens.ErrTokenConsumed):
		response.WriteConflict(w, err.Error(), "", nil)
	default:
		response.WriteInternalError(w, "Failed to validate the approval link", err)
	}
}
