package server

import (
	_ "embed"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/server/middleware"
	"github.com/dtorcivia/schedlock/internal/web"
)

//go:embed SKILL.md
var skillMD []byte

// setupRoutes registers every HTTP route.
func (s *Server) setupRoutes() {
	s.router.HandleFunc("GET /health", s.handleHealth)
	s.router.HandleFunc("GET /api/health", s.handleHealth)

	// Decision callbacks authenticate with a token in the path, so they are
	// registered before (and outside) the API-key-protected subtree. Go's
	// pattern matching prefers these more specific patterns over /api/.
	s.apiHandler.RegisterCallbackRoutes(s.router)

	// API routes behind API key authentication and rate limiting.
	apiMux := http.NewServeMux()
	s.apiHandler.RegisterRoutes(apiMux)
	s.router.Handle("/api/", middleware.APIKeyAuth(s.webHandler.APIKeyRepo(), s.rateLimiter, s.usageRecorder)(apiMux))

	// Telegram authenticates with its own shared secret header.
	if path := s.config.Notifications.Telegram.WebhookPath; path != "" {
		s.router.Handle("POST "+path, s.telegramHandler)
	}

	s.webHandler.RegisterRoutes(s.router)

	// Machine-readable usage instructions for agents.
	s.router.HandleFunc("GET /SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		if _, err := w.Write(skillMD); err != nil {
			return
		}
	})

	s.router.Handle("GET /static/", web.StaticHandler())
}

// handleHealth reports whether the server can serve requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := s.db.PingContext(ctx); err != nil {
		response.JSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unhealthy",
			"error":  "database unavailable",
		})
		return
	}

	calendar := "not_configured"
	switch {
	case s.oauthMgr.HasToken(ctx):
		calendar = "connected"
	case s.oauthMgr.IsConfigured(ctx):
		calendar = "configured"
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"status":   "healthy",
		"version":  Version,
		"calendar": calendar,
	})
}
