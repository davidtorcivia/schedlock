package web

import (
	"net/http"
)

// RegisterRoutes registers the web UI routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Public routes.
	mux.HandleFunc("GET /login", h.Login)
	mux.Handle("POST /login", CSRFProtection(http.HandlerFunc(h.LoginSubmit)))
	mux.Handle("POST /logout", CSRFProtection(http.HandlerFunc(h.Logout)))

	// The approval page authenticates with the decision token in its URL, so it
	// needs no session. It is exempt from CSRF for the same reason: there is no
	// ambient authority to abuse, and the notification recipient may open the
	// link in any browser.
	mux.HandleFunc("GET /approve/{token}", h.PublicApprove)
	mux.HandleFunc("POST /approve/{token}", h.PublicApprove)

	// The OAuth provider redirects here; the state parameter authenticates it.
	mux.HandleFunc("GET /oauth/callback", h.OAuthCallback)

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	})

	// Authenticated routes. Every state-changing route is wrapped in CSRF
	// protection inside the session middleware, so the check can compare
	// against the session's own token.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /dashboard", h.Dashboard)
	protected.HandleFunc("GET /pending", h.PendingRequests)
	protected.HandleFunc("GET /requests/{requestId}", h.RequestDetail)
	protected.HandleFunc("POST /requests/{requestId}/approve", h.ApproveRequest)
	protected.HandleFunc("POST /requests/{requestId}/deny", h.DenyRequest)
	protected.HandleFunc("POST /requests/{requestId}/suggest", h.SuggestChange)
	protected.HandleFunc("POST /requests/{requestId}/update", h.UpdatePayload)
	protected.HandleFunc("GET /history", h.History)
	protected.HandleFunc("GET /apikeys", h.APIKeys)
	protected.HandleFunc("POST /apikeys", h.CreateAPIKey)
	protected.HandleFunc("POST /apikeys/{keyId}/revoke", h.RevokeAPIKey)
	protected.HandleFunc("GET /settings", h.Settings)
	protected.HandleFunc("POST /settings/save", h.SaveSettings)
	protected.HandleFunc("POST /settings/notifications", h.SaveNotificationSettings)
	protected.HandleFunc("POST /settings/google-oauth", h.SaveGoogleOAuthSettings)
	protected.HandleFunc("POST /settings/test-notification", h.TestNotification)
	protected.HandleFunc("GET /oauth/start", h.OAuthStart)

	guarded := h.SessionManager.RequireSession(CSRFProtection(protected))

	for _, pattern := range []string{
		"GET /dashboard",
		"GET /pending",
		"GET /requests/{requestId}",
		"POST /requests/{requestId}/{action}",
		"GET /history",
		"GET /apikeys",
		"POST /apikeys",
		"POST /apikeys/{keyId}/revoke",
		"GET /settings",
		"POST /settings/{action}",
		"GET /oauth/start",
	} {
		mux.Handle(pattern, guarded)
	}
}
