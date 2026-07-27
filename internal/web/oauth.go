package web

import (
	"errors"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/util"
)

// OAuthStart begins the Google authorization flow.
func (h *Handler) OAuthStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.OAuthManager.IsConfigured(ctx) {
		h.render(w, r, "oauth_not_configured.html", pageData{
			"Title":       "Connect Google Calendar",
			"ErrorTitle":  "Google OAuth is not configured",
			"RedirectURI": h.OAuthManager.RedirectURI(),
		})
		return
	}

	state, err := google.GenerateOAuthState()
	if err != nil {
		util.Error("Failed to generate OAuth state", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The authorization could not be started.")
		return
	}

	if err := h.OAuthManager.StoreOAuthState(ctx, state); err != nil {
		util.Error("Failed to store OAuth state", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The authorization could not be started.")
		return
	}

	authURL, err := h.OAuthManager.AuthCodeURL(ctx, state)
	if err != nil {
		util.Error("Failed to build authorization URL", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The authorization could not be started.")
		return
	}

	h.render(w, r, "oauth.html", pageData{
		"Title":       "Connect Google Calendar",
		"Nav":         "settings",
		"AuthURL":     authURL,
		"RedirectURI": h.OAuthManager.RedirectURI(),
	})
}

// OAuthCallback completes the authorization flow.
//
// The state parameter is validated before the code is exchanged, so a forged
// callback cannot bind an attacker's Google account to this installation.
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if authErr := r.FormValue("error"); authErr != "" {
		util.Warn("Google authorization was refused", "reason", authErr)
		h.renderOAuthError(w, r, "Authorization refused",
			"Google reported that access was not granted.")
		return
	}

	code := r.FormValue("code")
	state := r.FormValue("state")
	if code == "" || state == "" {
		h.renderOAuthError(w, r, "Incomplete authorization",
			"The response from Google was missing information. Please try again.")
		return
	}

	if err := h.OAuthManager.ValidateOAuthState(ctx, state); err != nil {
		util.Warn("OAuth state validation failed", "error", err, "client_ip", h.ClientIP(r))
		h.AuditLogger.Log(ctx, engine.Entry{
			EventType: database.AuditOAuthFailed,
			Actor:     "web",
			IPAddress: h.ClientIP(r),
			Details:   map[string]any{"reason": "state_validation_failed"},
		})
		h.renderOAuthError(w, r, "Authorization could not be verified", oauthStateMessage(err))
		return
	}

	if err := h.OAuthManager.ExchangeCode(ctx, code); err != nil {
		util.Error("Failed to exchange authorization code", "error", err)
		h.AuditLogger.Log(ctx, engine.Entry{
			EventType: database.AuditOAuthFailed,
			Actor:     "web",
			IPAddress: h.ClientIP(r),
			Details:   map[string]any{"reason": "code_exchange_failed"},
		})
		h.renderOAuthError(w, r, "Could not connect Google Calendar",
			"The authorization code could not be exchanged. Authorization codes expire quickly, so please try again.")
		return
	}

	// The Calendar service holds a client built from the previous credentials.
	if invalidator, ok := h.CalendarClient.(interface{ InvalidateService() }); ok {
		invalidator.InvalidateService()
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditOAuthConnected,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
	})

	http.Redirect(w, r, "/settings?saved=google_connected", http.StatusSeeOther)
}

func (h *Handler) renderOAuthError(w http.ResponseWriter, r *http.Request, title, message string) {
	h.renderStatus(w, r, http.StatusBadRequest, "oauth_not_configured.html", pageData{
		"Title":        title,
		"Nav":          "settings",
		"ErrorTitle":   title,
		"ErrorMessage": message,
		"RedirectURI":  h.OAuthManager.RedirectURI(),
	})
}

func oauthStateMessage(err error) string {
	switch {
	case errors.Is(err, google.ErrStateExpired):
		return "The authorization took too long to complete. Please start again."
	case errors.Is(err, google.ErrStateNotFound):
		return "No authorization was in progress. Please start again from Settings."
	default:
		return "The authorization response did not match the request. Please start again."
	}
}
