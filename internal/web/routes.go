package web

import (
	"net/http"
)

// RegisterRoutes registers web UI routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Public routes (no auth required)
	mux.HandleFunc("GET /login", h.Login)
	mux.Handle("POST /login", CSRFProtection(http.HandlerFunc(h.LoginSubmit)))
	mux.HandleFunc("GET /logout", h.Logout)
	mux.Handle("POST /logout", CSRFProtection(http.HandlerFunc(h.Logout)))

	// OAuth callback (special case - might need session or might be headless)
	mux.HandleFunc("GET /oauth/callback", h.OAuthCallback)
	mux.HandleFunc("POST /oauth/callback", h.OAuthCallback)

	// Protected routes - wrapped with session middleware
	protected := http.NewServeMux()

	// Dashboard
	protected.HandleFunc("GET /dashboard", h.Dashboard)

	// Pending approvals
	protected.HandleFunc("GET /pending", h.PendingRequests)
	protected.HandleFunc("GET /requests/{requestId}", h.RequestDetail)
	protected.HandleFunc("POST /requests/{requestId}/approve", h.ApproveRequest)
	protected.HandleFunc("POST /requests/{requestId}/deny", h.DenyRequest)
	protected.HandleFunc("POST /requests/{requestId}/suggest", h.SuggestChange)

	// History
	protected.HandleFunc("GET /history", h.History)

	// API Keys
	protected.HandleFunc("GET /apikeys", h.APIKeys)
	protected.HandleFunc("POST /apikeys", h.CreateAPIKey)
	protected.HandleFunc("POST /apikeys/{keyId}/revoke", h.RevokeAPIKey)

	// Settings
	protected.HandleFunc("GET /settings", h.Settings)
	protected.HandleFunc("POST /settings/test-notification", h.TestNotification)
	protected.HandleFunc("POST /settings/save", h.SaveSettings)
	protected.HandleFunc("POST /settings/notifications", h.SaveNotificationSettings)
	protected.HandleFunc("GET /oauth/start", h.OAuthStart)

	// Apply session middleware to protected routes
	protectedHandler := h.sessionMgr.RequireSession(CSRFProtection(protected))

	// Redirect root to dashboard
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	// Register the protected routes with the main mux
	mux.Handle("GET /dashboard", protectedHandler)
	mux.Handle("GET /pending", protectedHandler)
	mux.Handle("GET /requests/", protectedHandler)
	mux.Handle("POST /requests/", protectedHandler)
	mux.Handle("GET /history", protectedHandler)
	mux.Handle("GET /apikeys", protectedHandler)
	mux.Handle("POST /apikeys", protectedHandler)
	mux.Handle("POST /apikeys/", protectedHandler)
	mux.Handle("GET /settings", protectedHandler)
	mux.Handle("POST /settings/", protectedHandler)
	mux.Handle("GET /oauth/start", protectedHandler)
}
