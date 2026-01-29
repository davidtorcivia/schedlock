package api

import (
	"context"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/response"
)

// ApproveCallback handles approval via decision token.
func (h *Handler) ApproveCallback(w http.ResponseWriter, r *http.Request) {
	h.handleCallback(w, r, "approve")
}

// DenyCallback handles denial via decision token.
func (h *Handler) DenyCallback(w http.ResponseWriter, r *http.Request) {
	h.handleCallback(w, r, "deny")
}

// SuggestCallback handles suggestions via decision token.
func (h *Handler) SuggestCallback(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		response.Error(w, http.StatusBadRequest, "token required", nil)
		return
	}

	// Get suggestion from body or query
	var suggestion string
	if r.Method == http.MethodPost {
		var body struct {
			Suggestion string `json:"suggestion"`
		}
		if err := parseJSON(r, &body); err == nil {
			suggestion = body.Suggestion
		}
	}
	if suggestion == "" {
		suggestion = r.URL.Query().Get("suggestion")
	}
	if suggestion == "" {
		response.Error(w, http.StatusBadRequest, "suggestion text required", nil)
		return
	}

	ctx := r.Context()

	// Consume token
	requestID, err := h.tokenRepo.Consume(ctx, token, "suggest")
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Process suggestion
	err = h.engine.ProcessSuggestion(ctx, requestID, suggestion, "callback")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to process suggestion", err)
		return
	}

	// Check Accept header for response format
	if r.Header.Get("Accept") == "text/html" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Suggestion Submitted</title></head>
<body style="font-family: system-ui; padding: 40px; text-align: center;">
<h1>📝 Suggestion Submitted</h1>
<p>Your feedback has been recorded. The request has been updated.</p>
</body>
</html>`))
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"message":    "suggestion recorded",
		"request_id": requestID,
	})
}

// handleCallback processes approve/deny callbacks.
func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request, action string) {
	token := r.PathValue("token")
	if token == "" {
		response.Error(w, http.StatusBadRequest, "token required", nil)
		return
	}

	ctx := r.Context()

	// Consume token
	requestID, err := h.tokenRepo.Consume(ctx, token, action)
	if err != nil {
		// Check if HTML response desired
		if r.Header.Get("Accept") == "text/html" || r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Error</title></head>
<body style="font-family: system-ui; padding: 40px; text-align: center;">
<h1>❌ Error</h1>
<p>` + err.Error() + `</p>
<p>This link may have expired or already been used.</p>
</body>
</html>`))
			return
		}
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Process approval/denial
	err = h.engine.ProcessApproval(ctx, requestID, action, "callback")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to process "+action, err)
		return
	}

	// Check Accept header for response format
	if r.Header.Get("Accept") == "text/html" || r.Method == http.MethodGet {
		var emoji, title string
		if action == "approve" {
			emoji = "✅"
			title = "Approved"
		} else {
			emoji = "❌"
			title = "Denied"
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Request ` + title + `</title></head>
<body style="font-family: system-ui; padding: 40px; text-align: center;">
<h1>` + emoji + ` Request ` + title + `</h1>
<p>The request has been ` + action + `d successfully.</p>
</body>
</html>`))
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"message":    "request " + action + "d",
		"request_id": requestID,
	})
}

// HandleCallback implements the notifications.CallbackHandler interface.
// This allows notification providers to process callbacks.
func (h *Handler) HandleCallback(ctx context.Context, callback *notifications.Callback) error {
	switch callback.Action {
	case "approve", "deny":
		return h.engine.ProcessApproval(ctx, callback.RequestID, callback.Action, callback.RespondedBy)
	case "suggest":
		return h.engine.ProcessSuggestion(ctx, callback.RequestID, callback.Suggestion, callback.RespondedBy)
	default:
		return nil
	}
}
