package api

import (
	"errors"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/response"
)

// ListRequests returns the calling key's recent requests.
func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	records, err := h.requestRepo.GetByAPIKeyID(r.Context(), authKey.ID, parseLimit(r, 50, 200))
	if err != nil {
		response.WriteInternalError(w, "Failed to list requests", err)
		return
	}

	// An empty result is rendered as [] rather than null, so clients can
	// iterate the field unconditionally.
	items := make([]map[string]any, 0, len(records))
	for _, req := range records {
		item := map[string]any{
			"id":         req.ID,
			"operation":  req.Operation,
			"status":     req.Status,
			"created_at": req.CreatedAt,
			"expires_at": req.ExpiresAt,
		}
		if req.DecidedAt.Valid {
			item["decided_at"] = req.DecidedAt.Time
		}
		addIfValid(item, "decided_by", req.DecidedBy)
		if req.ExecutedAt.Valid {
			item["executed_at"] = req.ExecutedAt.Time
		}
		addIfValid(item, "error", req.Error)
		addIfValid(item, "suggestion", req.SuggestionText)
		items = append(items, item)
	}

	response.JSON(w, http.StatusOK, map[string]any{"requests": items})
}

// GetRequest returns one request belonging to the calling key.
func (h *Handler) GetRequest(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierRead)
	if authKey == nil {
		return
	}

	requestID := r.PathValue("requestId")
	if requestID == "" {
		response.WriteValidationError(w, "requestId is required")
		return
	}

	req, err := h.requestRepo.GetByID(r.Context(), requestID)
	if err != nil {
		response.WriteInternalError(w, "Failed to read the request", err)
		return
	}

	// A request belonging to another key is reported as missing rather than
	// forbidden, so an unrelated caller cannot probe for valid request IDs.
	if req == nil || (req.APIKeyID != authKey.ID && authKey.Tier != database.TierAdmin) {
		response.WriteRequestNotFound(w, requestID)
		return
	}

	body := map[string]any{
		"id":          req.ID,
		"operation":   req.Operation,
		"status":      req.Status,
		"payload":     req.Payload,
		"created_at":  req.CreatedAt,
		"expires_at":  req.ExpiresAt,
		"retry_count": req.RetryCount,
	}
	if len(req.Result) > 0 {
		body["result"] = req.Result
	}
	if req.DecidedAt.Valid {
		body["decided_at"] = req.DecidedAt.Time
	}
	addIfValid(body, "decided_by", req.DecidedBy)
	if req.ExecutedAt.Valid {
		body["executed_at"] = req.ExecutedAt.Time
	}
	addIfValid(body, "error", req.Error)
	if req.SuggestionText.Valid {
		suggestion := map[string]any{"text": req.SuggestionText.String}
		addIfValid(suggestion, "suggested_by", req.SuggestionBy)
		if req.SuggestionAt.Valid {
			suggestion["suggested_at"] = req.SuggestionAt.Time
		}
		body["suggestion"] = suggestion
	}

	response.JSON(w, http.StatusOK, body)
}

// CancelRequest withdraws a pending request.
func (h *Handler) CancelRequest(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, database.TierWrite)
	if authKey == nil {
		return
	}

	requestID := r.PathValue("requestId")
	if requestID == "" {
		response.WriteValidationError(w, "requestId is required")
		return
	}

	if err := h.engine.CancelRequest(r.Context(), requestID, authKey.ID); err != nil {
		if errors.Is(err, requests.ErrNotPending) {
			response.WriteConflict(w, "Request not found or no longer pending", requestID, nil)
			return
		}
		response.WriteInternalError(w, "Failed to cancel the request", err, "request_id", requestID)
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"message":    "Request cancelled",
		"request_id": requestID,
	})
}
