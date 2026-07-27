package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/util"
)

// APIKeys lists the issued API keys.
func (h *Handler) APIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.APIKeyRepo().List(r.Context(), false)
	if err != nil {
		util.Error("Failed to list API keys", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The API keys could not be loaded.")
		return
	}

	h.render(w, r, "apikeys.html", pageData{
		"Title": "API keys",
		"Nav":   "apikeys",
		"Keys":  keys,
	})
}

// CreateAPIKey issues a new API key.
//
// The generated key is rendered once, into the response body. It is never put
// in a redirect URL: query strings are written to server logs, proxy logs, and
// browser history, and a leaked key there is a working credential.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	name := util.SanitizeLine(r.FormValue("name"))
	tier := r.FormValue("tier")

	if name == "" {
		h.renderKeyList(w, r, http.StatusBadRequest, "", "A key name is required.")
		return
	}
	if err := util.ValidateLength("name", name, 128); err != nil {
		h.renderKeyList(w, r, http.StatusBadRequest, "", err.Error())
		return
	}
	switch tier {
	case database.TierRead, database.TierWrite, database.TierAdmin:
	default:
		h.renderKeyList(w, r, http.StatusBadRequest, "", "Choose a valid access tier.")
		return
	}

	apiKey, fullKey, err := h.APIKeyRepo().Create(ctx, name, tier, nil)
	if err != nil {
		util.Error("Failed to create API key", "error", err)
		h.renderKeyList(w, r, http.StatusInternalServerError, "", "The key could not be created.")
		return
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditAPIKeyCreated,
		APIKeyID:  apiKey.ID,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
		Details:   map[string]any{"name": name, "tier": tier},
	})

	h.renderKeyList(w, r, http.StatusOK, fullKey, "")
}

// RevokeAPIKey revokes a key.
func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := r.PathValue("keyId")

	if err := h.APIKeyRepo().Revoke(ctx, keyID); err != nil {
		if !errors.Is(err, apikeys.ErrKeyNotRevocable) {
			util.Error("Failed to revoke API key", "error", err, "key_id", keyID)
		}
		h.redirectBack(w, r, "/apikeys")
		return
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditAPIKeyRevoked,
		APIKeyID:  keyID,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
	})

	h.redirectBack(w, r, "/apikeys")
}

// renderKeyList re-renders the key page, optionally revealing a new key.
func (h *Handler) renderKeyList(w http.ResponseWriter, r *http.Request, status int, newKey, errMessage string) {
	keys, err := h.APIKeyRepo().List(r.Context(), false)
	if err != nil {
		util.Error("Failed to list API keys", "error", err)
	}

	data := pageData{
		"Title": "API keys",
		"Nav":   "apikeys",
		"Keys":  keys,
	}
	if newKey != "" {
		data["NewKey"] = newKey
	}
	if errMessage != "" {
		data["Error"] = errMessage
	}

	h.renderStatus(w, r, status, "apikeys.html", data)
}

// maskSecret renders a stored secret as a hint rather than its value.
//
// Settings pages previously echoed provider tokens back into the HTML, which
// put every notification credential into the page source (and into any cache or
// screenshot of it). The operator only needs to know a secret is set.
func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return strings.Repeat("•", 8)
	}
	return strings.Repeat("•", 8) + secret[len(secret)-4:]
}
