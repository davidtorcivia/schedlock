// Package server provides route registration for SchedLock.
package server

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/server/middleware"
)

//go:embed SKILL.md
var skillMD []byte

// setupRoutes registers all HTTP routes.
func (s *Server) setupRoutes() {
	// Health check (no auth required)
	s.router.HandleFunc("GET /health", s.handleHealth)
	s.router.HandleFunc("GET /api/health", s.handleHealth)

	// API routes with API key authentication
	apiMux := http.NewServeMux()
	s.apiHandler.RegisterRoutes(apiMux)

	// Wrap API routes with authentication and rate limiting
	apiHandler := middleware.APIKeyAuth(s.apiKeyRepo, s.rateLimiter)(apiMux)
	s.router.Handle("/api/{path...}", apiHandler)

	// Telegram webhook (special auth via bot token in URL)
	if s.telegramHandler != nil {
		s.router.Handle("POST "+s.config.Notifications.Telegram.WebhookPath, s.telegramHandler)
	}

	// Web UI routes
	s.webHandler.RegisterRoutes(s.router)

	// SKILL.md for agent discovery
	s.router.HandleFunc("GET /SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(skillMD)
	})

	// Static files
	s.router.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
}

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check database connectivity
	if err := s.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "unhealthy",
			"error":  "database unavailable",
		})
		return
	}

	// Check if OAuth is configured
	oauthStatus := "not_configured"
	if s.oauthMgr.IsAuthenticated() {
		oauthStatus = "connected"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
		"oauth":   oauthStatus,
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
