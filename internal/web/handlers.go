// Package web serves the administrative UI and the public approval page.
package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// CalendarReader is the calendar behaviour the UI needs.
type CalendarReader interface {
	GetEvent(ctx context.Context, calendarID, eventID string) (*google.Event, error)
}

// Dependencies collects everything the web handler needs.
type Dependencies struct {
	Config           *config.Config
	SessionManager   *SessionManager
	RequestRepo      *requests.Repository
	APIKeyRepo       *apikeys.Repository
	TokenRepo        *tokens.Repository
	SettingsStore    *settings.Store
	CredentialsStore *notifications.CredentialsStore
	Engine           *engine.Engine
	OAuthManager     *google.OAuthManager
	CalendarClient   CalendarReader
	NotificationMgr  *notifications.Manager
	AuditLogger      *engine.AuditLogger
	ClientIP         func(*http.Request) string
}

// Handler serves the web UI.
type Handler struct {
	Dependencies
	templates    *TemplateSet
	loginLimiter *AttemptLimiter
	pinLimiter   *AttemptLimiter
}

// NewHandler creates the web handler.
func NewHandler(deps Dependencies) (*Handler, error) {
	templates, err := LoadTemplates()
	if err != nil {
		return nil, err
	}

	return &Handler{
		Dependencies: deps,
		templates:    templates,
		// Both limiters exist to make guessing expensive: the admin password
		// protects the whole UI, and the approval PIN is only 4-8 digits, so an
		// unthrottled endpoint would be brute-forced in minutes.
		loginLimiter: NewAttemptLimiter(10, 10*time.Minute),
		pinLimiter:   NewAttemptLimiter(5, 10*time.Minute),
	}, nil
}

// APIKeyRepo exposes the API key repository for route wiring.
func (h *Handler) APIKeyRepo() *apikeys.Repository { return h.Dependencies.APIKeyRepo }

// pageData is the data every page receives.
type pageData map[string]any

// render writes an authenticated page.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data pageData) {
	h.renderStatus(w, r, http.StatusOK, page, data)
}

func (h *Handler) renderStatus(w http.ResponseWriter, r *http.Request, status int, page string, data pageData) {
	if data == nil {
		data = pageData{}
	}

	session := SessionFrom(r.Context())
	if session != nil {
		data["Session"] = session
		data["CSRFToken"] = session.CSRFToken
	} else if _, ok := data["CSRFToken"]; !ok {
		// Pre-session pages (login, setup) still need a token; it is bound to
		// the browser through the CSRF cookie set below.
		token, cookie := h.issueAnonymousCSRF(w)
		data["CSRFToken"] = token
		_ = cookie
	}

	data["BaseURL"] = h.Config.Server.BaseURL
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = ""
	}

	h.templates.Render(w, status, page, data)
}

// Dashboard shows the operational overview.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := h.RequestRepo.GetStats(ctx)
	if err != nil {
		util.Error("Failed to read request statistics", "error", err)
	}

	keyCounts, err := h.APIKeyRepo().Count(ctx)
	if err != nil {
		util.Error("Failed to count API keys", "error", err)
	}
	totalKeys := 0
	for _, count := range keyCounts {
		totalKeys += count
	}

	pending, err := h.RequestRepo.GetPending(ctx)
	if err != nil {
		util.Error("Failed to list pending requests", "error", err)
	}

	h.render(w, r, "dashboard.html", pageData{
		"Title":        "Dashboard",
		"Nav":          "dashboard",
		"Stats":        stats,
		"APIKeyCounts": keyCounts,
		"APIKeyTotal":  totalKeys,
		"Pending":      summarizeRequests(pending),
	})
}

// PendingRequests lists everything awaiting a decision.
func (h *Handler) PendingRequests(w http.ResponseWriter, r *http.Request) {
	pending, err := h.RequestRepo.GetPending(r.Context())
	if err != nil {
		util.Error("Failed to list pending requests", "error", err)
	}

	h.render(w, r, "pending.html", pageData{
		"Title":    "Pending approvals",
		"Nav":      "pending",
		"Requests": summarizeRequests(pending),
	})
}

// RequestDetail shows one request in full.
func (h *Handler) RequestDetail(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	ctx := r.Context()

	req, err := h.RequestRepo.GetByID(ctx, requestID)
	if err != nil {
		util.Error("Failed to read request", "error", err, "request_id", requestID)
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong",
			"The request could not be loaded.")
		return
	}
	if req == nil {
		h.renderError(w, r, http.StatusNotFound, "Request not found",
			"This request may have been removed by the retention policy.")
		return
	}

	auditEntries, err := h.AuditLogger.GetByRequestID(ctx, requestID)
	if err != nil {
		util.Warn("Failed to read request history", "error", err, "request_id", requestID)
	}

	h.render(w, r, "detail.html", pageData{
		"Title":   "Request details",
		"Nav":     "pending",
		"Request": newRequestView(req),
		"Form":    newEventForm(req),
		"History": summarizeAudit(auditEntries),
	})
}

// History shows the audit trail.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	entries, err := h.AuditLogger.GetRecent(r.Context(), 200)
	if err != nil {
		util.Error("Failed to read audit history", "error", err)
	}

	h.render(w, r, "history.html", pageData{
		"Title":   "Audit history",
		"Nav":     "history",
		"Entries": summarizeAudit(entries),
	})
}

// ApproveRequest approves a request from the admin UI.
func (h *Handler) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, "approve")
}

// DenyRequest denies a request from the admin UI.
func (h *Handler) DenyRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, "deny")
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, action string) {
	requestID := r.PathValue("requestId")

	if err := h.Engine.ProcessApproval(r.Context(), requestID, action, h.actor(r)); err != nil {
		util.Warn("Decision rejected", "error", err, "request_id", requestID, "action", action)
		h.redirectBack(w, r, "/pending")
		return
	}

	h.redirectBack(w, r, "/pending")
}

// SuggestChange records requested changes from the admin UI.
func (h *Handler) SuggestChange(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	suggestion := r.FormValue("suggestion")

	if err := h.Engine.ProcessSuggestion(r.Context(), requestID, suggestion, h.actor(r)); err != nil {
		util.Warn("Suggestion rejected", "error", err, "request_id", requestID)
	}

	h.redirectBack(w, r, "/pending")
}

// actor identifies who performed an action, for the audit trail.
func (h *Handler) actor(r *http.Request) string {
	session := SessionFrom(r.Context())
	if session == nil {
		return "web"
	}
	return "web:" + session.UserID
}

// redirectBack sends the browser onward, honouring htmx when it is driving.
func (h *Handler) redirectBack(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// renderError shows an error page inside the normal layout.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	h.renderStatus(w, r, status, "oauth_not_configured.html", pageData{
		"Title":        title,
		"ErrorTitle":   title,
		"ErrorMessage": message,
	})
}

// issueAnonymousCSRF mints a CSRF token for a page with no session yet.
func (h *Handler) issueAnonymousCSRF(w http.ResponseWriter) (string, bool) {
	token, err := GenerateCSRFToken()
	if err != nil {
		util.Error("Failed to generate CSRF token", "error", err)
		return "", false
	}
	SetCSRFCookie(w, token, h.useTLS(), csrfCookieLifetime)
	return token, true
}

func (h *Handler) useTLS() bool {
	return strings.HasPrefix(h.Config.Server.BaseURL, "https://")
}
