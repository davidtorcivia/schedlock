// Package api provides REST API handlers.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/server/middleware"
	"github.com/dtorcivia/schedlock/internal/tokens"
)

// Handler provides REST API handlers.
type Handler struct {
	config          *config.Config
	engine          *engine.Engine
	requestRepo     *requests.Repository
	apiKeyRepo      *apikeys.Repository
	tokenRepo       *tokens.Repository
	calendarClient  CalendarClient
	notificationMgr *notifications.Manager
	auditLogger     *engine.AuditLogger
}

// CalendarClient defines the subset of Google Calendar client behavior used by the API handler.
type CalendarClient interface {
	ListCalendars(ctx context.Context) ([]google.Calendar, error)
	ListEvents(ctx context.Context, opts google.EventListOptions) (*google.EventListResponse, error)
	GetEvent(ctx context.Context, calendarID, eventID string) (*google.Event, error)
	FreeBusy(ctx context.Context, req *google.FreeBusyRequest) (*google.FreeBusyResponse, error)
	CreateEvent(ctx context.Context, intent *google.EventIntent) (*google.Event, error)
	UpdateEvent(ctx context.Context, intent *google.EventUpdateIntent) (*google.Event, error)
	DeleteEvent(ctx context.Context, intent *google.EventDeleteIntent) error
}

// NewHandler creates a new API handler.
func NewHandler(
	cfg *config.Config,
	eng *engine.Engine,
	requestRepo *requests.Repository,
	apiKeyRepo *apikeys.Repository,
	tokenRepo *tokens.Repository,
	calendarClient CalendarClient,
	notificationMgr *notifications.Manager,
	auditLogger *engine.AuditLogger,
) *Handler {
	return &Handler{
		config:          cfg,
		engine:          eng,
		requestRepo:     requestRepo,
		apiKeyRepo:      apiKeyRepo,
		tokenRepo:       tokenRepo,
		calendarClient:  calendarClient,
		notificationMgr: notificationMgr,
		auditLogger:     auditLogger,
	}
}

// RegisterRoutes registers API routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Health check (no auth)
	mux.HandleFunc("GET /api/health", h.Health)

	// Calendar read operations (read tier)
	mux.HandleFunc("GET /api/calendar/list", h.ListCalendars)
	mux.HandleFunc("GET /api/calendar/{calendarId}/events", h.ListEvents)
	mux.HandleFunc("GET /api/calendar/{calendarId}/events/{eventId}", h.GetEvent)
	mux.HandleFunc("GET /api/calendar/freebusy", h.FreeBusy)
	mux.HandleFunc("POST /api/calendar/freebusy", h.FreeBusy)

	// Calendar write operations (write tier)
	mux.HandleFunc("POST /api/calendar/events/create", h.CreateEvent)
	mux.HandleFunc("POST /api/calendar/events/update", h.UpdateEvent)
	mux.HandleFunc("POST /api/calendar/events/delete", h.DeleteEvent)

	// Request management
	mux.HandleFunc("GET /api/requests", h.ListRequests)
	mux.HandleFunc("GET /api/requests/{requestId}", h.GetRequest)
	mux.HandleFunc("POST /api/requests/{requestId}/cancel", h.CancelRequest)

	// Callback endpoints (token-based auth)
	mux.HandleFunc("POST /api/callback/approve/{token}", h.ApproveCallback)
	mux.HandleFunc("POST /api/callback/deny/{token}", h.DenyCallback)
	mux.HandleFunc("POST /api/callback/suggest/{token}", h.SuggestCallback)
	mux.HandleFunc("GET /api/callback/approve/{token}", h.ApproveCallback)
	mux.HandleFunc("GET /api/callback/deny/{token}", h.DenyCallback)

	// Admin endpoints (admin tier)
	mux.HandleFunc("GET /api/admin/stats", h.GetStats)
	mux.HandleFunc("GET /api/admin/audit", h.GetAuditLog)
}

// Health returns server health status.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
	})
}

// GetStats returns system statistics.
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	// Require admin tier
	authKey := middleware.GetAuthenticatedKey(r)
	if authKey == nil || authKey.Tier != "admin" {
		response.Error(w, http.StatusForbidden, "admin access required", nil)
		return
	}

	ctx := r.Context()

	stats, err := h.requestRepo.GetStats(ctx)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get stats", err)
		return
	}

	apiKeyStats, err := h.apiKeyRepo.Count(ctx)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get API key stats", err)
		return
	}

	auditCount, err := h.auditLogger.Count(ctx)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get audit count", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"requests":      stats,
		"api_keys":      apiKeyStats,
		"audit_entries": auditCount,
	})
}

// GetAuditLog returns recent audit entries.
func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	// Require admin tier
	authKey := middleware.GetAuthenticatedKey(r)
	if authKey == nil || authKey.Tier != "admin" {
		response.Error(w, http.StatusForbidden, "admin access required", nil)
		return
	}

	ctx := r.Context()

	entries, err := h.auditLogger.GetRecent(ctx, 100)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to get audit log", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
	})
}

// parseJSON decodes JSON request body.
func parseJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// requireTier checks if the authenticated key has at least the required tier.
func requireTier(w http.ResponseWriter, r *http.Request, requiredTier string) *apikeys.AuthenticatedKey {
	authKey := middleware.GetAuthenticatedKey(r)
	if authKey == nil {
		response.Error(w, http.StatusUnauthorized, "authentication required", nil)
		return nil
	}

	tierRank := map[string]int{"read": 1, "write": 2, "admin": 3}
	if tierRank[authKey.Tier] < tierRank[requiredTier] {
		response.Error(w, http.StatusForbidden, requiredTier+" tier required", nil)
		return nil
	}

	return authKey
}

// AuditEventTypes are the audit event type constants.
var (
	_ = database.AuditRequestCreated
	_ = database.AuditRequestApproved
)
