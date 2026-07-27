// Package api provides the REST API that agents call.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

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

// maxRequestBytes bounds a JSON request body. Calendar intents are small; an
// unbounded decode would let one caller exhaust memory.
const maxRequestBytes = 256 << 10

// CalendarClient is the calendar behaviour the API layer depends on.
type CalendarClient interface {
	ListCalendars(ctx context.Context) ([]google.Calendar, error)
	ListEvents(ctx context.Context, opts google.EventListOptions) (*google.EventListResponse, error)
	GetEvent(ctx context.Context, calendarID, eventID string) (*google.Event, error)
	FreeBusy(ctx context.Context, req *google.FreeBusyRequest) (*google.FreeBusyResponse, error)
}

// Handler serves the REST API.
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

// NewHandler creates an API handler.
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

// RegisterRoutes registers the authenticated API routes.
//
// Decision callbacks are deliberately absent: they authenticate with a decision
// token rather than an API key, and are registered on the public router.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Calendar reads.
	mux.HandleFunc("GET /api/calendar/list", h.ListCalendars)
	mux.HandleFunc("GET /api/calendar/{calendarId}/events", h.ListEvents)
	mux.HandleFunc("GET /api/calendar/{calendarId}/events/{eventId}", h.GetEvent)
	mux.HandleFunc("GET /api/calendar/freebusy", h.FreeBusy)
	mux.HandleFunc("POST /api/calendar/freebusy", h.FreeBusy)

	// Calendar writes, which enter the approval workflow.
	mux.HandleFunc("POST /api/calendar/events/create", h.CreateEvent)
	mux.HandleFunc("POST /api/calendar/events/update", h.UpdateEvent)
	mux.HandleFunc("POST /api/calendar/events/delete", h.DeleteEvent)

	// Request management.
	mux.HandleFunc("GET /api/requests", h.ListRequests)
	mux.HandleFunc("GET /api/requests/{requestId}", h.GetRequest)
	mux.HandleFunc("POST /api/requests/{requestId}/cancel", h.CancelRequest)

	// Administration.
	mux.HandleFunc("GET /api/admin/stats", h.GetStats)
	mux.HandleFunc("GET /api/admin/audit", h.GetAuditLog)
}

// GetStats returns system statistics.
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	if requireTier(w, r, database.TierAdmin) == nil {
		return
	}

	ctx := r.Context()

	stats, err := h.requestRepo.GetStats(ctx)
	if err != nil {
		response.WriteInternalError(w, "Failed to read request statistics", err)
		return
	}

	apiKeyStats, err := h.apiKeyRepo.Count(ctx)
	if err != nil {
		response.WriteInternalError(w, "Failed to read API key statistics", err)
		return
	}

	auditCount, err := h.auditLogger.Count(ctx)
	if err != nil {
		response.WriteInternalError(w, "Failed to read audit statistics", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"requests": map[string]any{
			"by_status":     stats.StatusCounts,
			"pending":       stats.TotalPending,
			"last_24_hours": stats.TotalLastDay,
		},
		"api_keys":      apiKeyStats,
		"audit_entries": auditCount,
	})
}

// GetAuditLog returns recent audit entries.
func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	if requireTier(w, r, database.TierAdmin) == nil {
		return
	}

	entries, err := h.auditLogger.GetRecent(r.Context(), parseLimit(r, 100, 1000))
	if err != nil {
		response.WriteInternalError(w, "Failed to read audit log", err)
		return
	}

	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{
			"id":         entry.ID,
			"timestamp":  entry.Timestamp,
			"event_type": entry.EventType,
		}
		addIfValid(item, "request_id", entry.RequestID)
		addIfValid(item, "api_key_id", entry.APIKeyID)
		addIfValid(item, "actor", entry.Actor)
		addIfValid(item, "ip_address", entry.IPAddress)
		if len(entry.Details) > 0 {
			item["details"] = entry.Details
		}
		items = append(items, item)
	}

	response.JSON(w, http.StatusOK, map[string]any{"entries": items})
}

// decodeJSON reads a bounded, strict JSON body.
//
// Unknown fields are rejected so a caller that misspells "attendees" is told,
// rather than silently having an approval request created without the guests
// they meant to invite.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		return err
	}
	return nil
}

// requireTier enforces a minimum tier, writing the error response itself.
func requireTier(w http.ResponseWriter, r *http.Request, requiredTier string) *apikeys.AuthenticatedKey {
	authKey := middleware.GetAuthenticatedKey(r)
	if authKey == nil {
		response.WriteInvalidAPIKey(w)
		return nil
	}

	rank := map[string]int{
		database.TierRead:  1,
		database.TierWrite: 2,
		database.TierAdmin: 3,
	}
	if rank[authKey.Tier] < rank[requiredTier] {
		response.WriteInsufficientPermissions(w, authKey.Tier, r.URL.Path)
		return nil
	}

	return authKey
}

// HandleCallback implements notifications.CallbackHandler, letting a provider
// deliver a decision made in a chat client.
func (h *Handler) HandleCallback(ctx context.Context, callback *notifications.Callback) error {
	if h.notificationMgr != nil {
		h.notificationMgr.MarkCallback(ctx, callback.Provider, callback.RequestID, callback.MessageID)
	}

	switch callback.Action {
	case "approve", "deny":
		return h.engine.ProcessApproval(ctx, callback.RequestID, callback.Action, callback.RespondedBy)
	case "suggest":
		return h.engine.ProcessSuggestion(ctx, callback.RequestID, callback.Suggestion, callback.RespondedBy)
	default:
		return fmt.Errorf("%w: %s", engine.ErrInvalidAction, callback.Action)
	}
}

// writeDecisionError maps an approval failure onto an HTTP response.
func writeDecisionError(w http.ResponseWriter, requestID string, err error) {
	var alreadyDecided *engine.ErrAlreadyDecided
	switch {
	case errors.As(err, &alreadyDecided):
		response.WriteConflict(w, alreadyDecided.Error(), requestID,
			map[string]any{"status": alreadyDecided.Status})
	case errors.Is(err, engine.ErrRequestNotFound):
		response.WriteRequestNotFound(w, requestID)
	case errors.Is(err, engine.ErrInvalidAction):
		response.WriteValidationError(w, err.Error())
	case errors.Is(err, requests.ErrNotPending):
		response.WriteConflict(w, "Request is no longer pending", requestID, nil)
	default:
		response.WriteInternalError(w, "Failed to process the decision", err, "request_id", requestID)
	}
}

// addIfValid copies a nullable column into a response map only when set, so
// absent values are omitted rather than rendered as empty strings.
func addIfValid(m map[string]any, key string, value sql.NullString) {
	if value.Valid && value.String != "" {
		m[key] = value.String
	}
}

// parseLimit reads a bounded "limit" query parameter.
func parseLimit(r *http.Request, fallback, maximum int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return fallback
	}
	if limit > maximum {
		return maximum
	}
	return limit
}
