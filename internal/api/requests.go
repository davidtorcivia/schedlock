package api

import (
	"net/http"

	"github.com/dtorcivia/schedlock/internal/response"
)

// ListRequests returns requests for the authenticated API key.
func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	ctx := r.Context()
	requests, err := h.requestRepo.GetByAPIKeyID(ctx, authKey.ID, 50)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to list requests", err)
		return
	}

	// Convert to response format
	var items []map[string]interface{}
	for _, req := range requests {
		item := map[string]interface{}{
			"id":         req.ID,
			"operation":  req.Operation,
			"status":     req.Status,
			"created_at": req.CreatedAt,
			"expires_at": req.ExpiresAt,
		}

		if req.DecidedAt.Valid {
			item["decided_at"] = req.DecidedAt.Time
		}
		if req.DecidedBy.Valid {
			item["decided_by"] = req.DecidedBy.String
		}
		if req.ExecutedAt.Valid {
			item["executed_at"] = req.ExecutedAt.Time
		}
		if req.Error.Valid {
			item["error"] = req.Error.String
		}
		if req.SuggestionText.Valid {
			item["suggestion"] = req.SuggestionText.String
		}

		items = append(items, item)
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"requests": items,
	})
}

// GetRequest returns a specific request.
func (h *Handler) GetRequest(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "read")
	if authKey == nil {
		return
	}

	requestID := r.PathValue("requestId")
	if requestID == "" {
		response.Error(w, http.StatusBadRequest, "request ID required", nil)
		return
	}

	ctx := r.Context()
	req, err := h.requestRepo.GetByID(ctx, requestID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get request", err)
		return
	}

	if req == nil {
		response.Error(w, http.StatusNotFound, "request not found", nil)
		return
	}

	// Only allow access to own requests (unless admin)
	if req.APIKeyID != authKey.ID && authKey.Tier != "admin" {
		response.Error(w, http.StatusForbidden, "access denied", nil)
		return
	}

	resp := map[string]interface{}{
		"id":          req.ID,
		"operation":   req.Operation,
		"status":      req.Status,
		"payload":     req.Payload,
		"created_at":  req.CreatedAt,
		"expires_at":  req.ExpiresAt,
		"retry_count": req.RetryCount,
	}

	if req.Result != nil {
		resp["result"] = req.Result
	}
	if req.DecidedAt.Valid {
		resp["decided_at"] = req.DecidedAt.Time
	}
	if req.DecidedBy.Valid {
		resp["decided_by"] = req.DecidedBy.String
	}
	if req.ExecutedAt.Valid {
		resp["executed_at"] = req.ExecutedAt.Time
	}
	if req.Error.Valid {
		resp["error"] = req.Error.String
	}
	if req.SuggestionText.Valid {
		resp["suggestion"] = map[string]interface{}{
			"text":         req.SuggestionText.String,
			"suggested_by": req.SuggestionBy.String,
			"suggested_at": req.SuggestionAt.Time,
		}
	}

	response.JSON(w, http.StatusOK, resp)
}

// CancelRequest cancels a pending request.
func (h *Handler) CancelRequest(w http.ResponseWriter, r *http.Request) {
	authKey := requireTier(w, r, "write")
	if authKey == nil {
		return
	}

	requestID := r.PathValue("requestId")
	if requestID == "" {
		response.Error(w, http.StatusBadRequest, "request ID required", nil)
		return
	}

	ctx := r.Context()
	err := h.engine.CancelRequest(ctx, requestID, authKey.ID)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"message": "request cancelled",
	})
}
