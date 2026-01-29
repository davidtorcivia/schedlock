package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Handler provides web UI handlers.
type Handler struct {
	config          *config.Config
	templates       *template.Template
	sessionMgr      *SessionManager
	requestRepo     *requests.Repository
	apiKeyRepo      *apikeys.Repository
	tokenRepo       *tokens.Repository
	engine          *engine.Engine
	oauthMgr        *google.OAuthManager
	notificationMgr *notifications.Manager
	auditLogger     *engine.AuditLogger
}

// NewHandler creates a new web handler.
func NewHandler(
	cfg *config.Config,
	sessionMgr *SessionManager,
	requestRepo *requests.Repository,
	apiKeyRepo *apikeys.Repository,
	tokenRepo *tokens.Repository,
	eng *engine.Engine,
	oauthMgr *google.OAuthManager,
	notificationMgr *notifications.Manager,
	auditLogger *engine.AuditLogger,
) (*Handler, error) {
	// Load templates from default location
	tmpl, err := loadTemplates("web/templates")
	if err != nil {
		return nil, err
	}

	return &Handler{
		config:          cfg,
		templates:       tmpl,
		sessionMgr:      sessionMgr,
		requestRepo:     requestRepo,
		apiKeyRepo:      apiKeyRepo,
		tokenRepo:       tokenRepo,
		engine:          eng,
		oauthMgr:        oauthMgr,
		notificationMgr: notificationMgr,
		auditLogger:     auditLogger,
	}, nil
}

// loadTemplates loads all HTML templates.
func loadTemplates(dir string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"formatTime": func(t time.Time) string {
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		"formatDate": func(t time.Time) string {
			return t.Format("Jan 2, 2006")
		},
		"formatJSON": func(v interface{}) string {
			data, _ := json.MarshalIndent(v, "", "  ")
			return string(data)
		},
		"statusClass": func(status string) string {
			switch status {
			case "pending_approval":
				return "status-pending"
			case "approved", "completed":
				return "status-success"
			case "denied", "cancelled":
				return "status-error"
			case "expired", "failed":
				return "status-warning"
			default:
				return "status-info"
			}
		},
		"statusIcon": func(status string) string {
			switch status {
			case "pending_approval":
				return "⏳"
			case "approved":
				return "👍"
			case "completed":
				return "✅"
			case "denied":
				return "❌"
			case "cancelled":
				return "🚫"
			case "expired":
				return "⏰"
			case "failed":
				return "💥"
			case "change_requested":
				return "📝"
			default:
				return "❓"
			}
		},
		"json": func(v interface{}) template.JS {
			data, _ := json.Marshal(v)
			return template.JS(data)
		},
	}

	pattern := filepath.Join(dir, "*.html")
	return template.New("").Funcs(funcMap).ParseGlob(pattern)
}

// render renders a template with common data.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}

	// Add common data
	session := GetSession(r.Context())
	if session != nil {
		data["Session"] = session
	}

	// Add CSRF token
	csrfToken, _ := GenerateCSRFToken()
	useTLS := strings.HasPrefix(h.config.Server.BaseURL, "https://")
	SetCSRFCookie(w, csrfToken, useTLS)
	data["CSRFToken"] = csrfToken

	// Add config data
	data["BaseURL"] = h.config.Server.BaseURL

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		util.Error("Template error", "template", name, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Login page
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in
	sessionID := GetSessionID(r)
	if sessionID != "" {
		session, _ := h.sessionMgr.ValidateSession(r.Context(), sessionID)
		if session != nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	h.render(w, r, "login.html", nil)
}

// LoginSubmit handles login form submission.
func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")

	if !h.sessionMgr.VerifyPassword(password) {
		h.render(w, r, "login.html", map[string]interface{}{
			"Error": "Invalid password",
		})
		return
	}

	// Create session
	ipAddress := r.RemoteAddr
	userAgent := r.UserAgent()

	session, err := h.sessionMgr.CreateSession(r.Context(), "admin", ipAddress, userAgent)
	if err != nil {
		h.render(w, r, "login.html", map[string]interface{}{
			"Error": "Failed to create session",
		})
		return
	}

	useTLS := strings.HasPrefix(h.config.Server.BaseURL, "https://")
	SetSessionCookie(w, session.ID, useTLS)

	// Redirect to dashboard
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// Logout handles logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sessionID := GetSessionID(r)
	if sessionID != "" {
		h.sessionMgr.DeleteSession(r.Context(), sessionID)
	}
	ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// Dashboard shows the main dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stats
	stats, _ := h.requestRepo.GetStats(ctx)
	apiKeyStats, _ := h.apiKeyRepo.Count(ctx)

	// Get pending requests
	pending, _ := h.requestRepo.GetPending(ctx)

	h.render(w, r, "dashboard.html", map[string]interface{}{
		"Stats":          stats,
		"APIKeyStats":    apiKeyStats,
		"PendingCount":   len(pending),
		"PendingRequests": pending,
	})
}

// PendingRequests shows pending approval requests.
func (h *Handler) PendingRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := h.requestRepo.GetPending(ctx)

	h.render(w, r, "pending.html", map[string]interface{}{
		"Requests": pending,
	})
}

// RequestDetail shows a specific request.
func (h *Handler) RequestDetail(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	if requestID == "" {
		http.Error(w, "Request ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	req, err := h.requestRepo.GetByID(ctx, requestID)
	if err != nil || req == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	// Get audit log for this request
	auditEntries, _ := h.auditLogger.GetByRequestID(ctx, requestID)

	// Parse payload for display
	var payload interface{}
	json.Unmarshal(req.Payload, &payload)

	h.render(w, r, "detail.html", map[string]interface{}{
		"Request":      req,
		"Payload":      payload,
		"AuditEntries": auditEntries,
	})
}

// ApproveRequest handles approval from web UI.
func (h *Handler) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	session := GetSession(r.Context())

	decidedBy := "web:admin"
	if session != nil {
		decidedBy = "web:" + session.UserID
	}

	if err := h.engine.ProcessApproval(r.Context(), requestID, "approve", decidedBy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/pending")
		return
	}

	http.Redirect(w, r, "/pending", http.StatusSeeOther)
}

// DenyRequest handles denial from web UI.
func (h *Handler) DenyRequest(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	session := GetSession(r.Context())

	decidedBy := "web:admin"
	if session != nil {
		decidedBy = "web:" + session.UserID
	}

	if err := h.engine.ProcessApproval(r.Context(), requestID, "deny", decidedBy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/pending")
		return
	}

	http.Redirect(w, r, "/pending", http.StatusSeeOther)
}

// SuggestChange handles suggestions from web UI.
func (h *Handler) SuggestChange(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("requestId")
	suggestion := r.FormValue("suggestion")
	session := GetSession(r.Context())

	suggestedBy := "web:admin"
	if session != nil {
		suggestedBy = "web:" + session.UserID
	}

	if err := h.engine.ProcessSuggestion(r.Context(), requestID, suggestion, suggestedBy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/pending")
		return
	}

	http.Redirect(w, r, "/pending", http.StatusSeeOther)
}

// History shows audit log.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entries, _ := h.auditLogger.GetRecent(ctx, 100)

	h.render(w, r, "history.html", map[string]interface{}{
		"Entries": entries,
	})
}

// APIKeys shows API key management.
func (h *Handler) APIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, _ := h.apiKeyRepo.List(ctx, false)

	h.render(w, r, "apikeys.html", map[string]interface{}{
		"Keys": keys,
	})
}

// CreateAPIKey creates a new API key.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	tier := r.FormValue("tier")

	if name == "" || tier == "" {
		http.Error(w, "Name and tier required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	apiKey, fullKey, err := h.apiKeyRepo.Create(ctx, name, tier, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Log to audit
	h.auditLogger.Log(ctx, database.AuditAPIKeyCreated, "", apiKey.ID, "web:admin", map[string]interface{}{
		"name": name,
		"tier": tier,
	})

	// If HTMX request, return the new key display
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<div class="alert alert-success">
			<p><strong>API Key Created!</strong></p>
			<p>Copy this key now - it won't be shown again:</p>
			<code class="key-display">` + fullKey + `</code>
		</div>`))
		return
	}

	http.Redirect(w, r, "/apikeys?created="+fullKey, http.StatusSeeOther)
}

// RevokeAPIKey revokes an API key.
func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	keyID := r.PathValue("keyId")

	ctx := r.Context()
	if err := h.apiKeyRepo.Revoke(ctx, keyID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Log to audit
	h.auditLogger.Log(ctx, database.AuditAPIKeyRevoked, "", keyID, "web:admin", nil)

	// If HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/apikeys")
		return
	}

	http.Redirect(w, r, "/apikeys", http.StatusSeeOther)
}

// Settings shows settings page.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	providers := h.notificationMgr.GetProviders()
	oauthConnected := h.oauthMgr.IsAuthenticated()

	h.render(w, r, "settings.html", map[string]interface{}{
		"Providers":      providers,
		"OAuthConnected": oauthConnected,
		"Config":         h.config,
	})
}

// TestNotification tests a notification provider.
func (h *Handler) TestNotification(w http.ResponseWriter, r *http.Request) {
	provider := r.FormValue("provider")

	ctx := r.Context()
	if err := h.notificationMgr.TestProvider(ctx, provider); err != nil {
		http.Error(w, "Test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Test notification sent successfully"))
}

// OAuthStart initiates OAuth flow.
func (h *Handler) OAuthStart(w http.ResponseWriter, r *http.Request) {
	authInfo := h.oauthMgr.GetAuthURLForHeadless("schedlock")

	h.render(w, r, "oauth.html", map[string]interface{}{
		"AuthURL":      authInfo.AuthURL,
		"Instructions": authInfo.Instructions,
	})
}

// OAuthCallback handles OAuth callback.
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	if code == "" {
		code = r.URL.Query().Get("code")
	}

	if code == "" {
		http.Error(w, "Authorization code required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if err := h.oauthMgr.ExchangeCodeManual(ctx, code); err != nil {
		h.render(w, r, "oauth.html", map[string]interface{}{
			"Error": "Failed to exchange code: " + err.Error(),
		})
		return
	}

	http.Redirect(w, r, "/settings?oauth=success", http.StatusSeeOther)
}
