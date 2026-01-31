package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Handler provides web UI handlers.
type Handler struct {
	config           *config.Config
	templates        *template.Template
	sessionMgr       *SessionManager
	loginLimiter     *LoginLimiter
	settingsStore    *settings.Store
	credentialsStore *notifications.CredentialsStore
	requestRepo      *requests.Repository
	apiKeyRepo       *apikeys.Repository
	tokenRepo        *tokens.Repository
	engine           *engine.Engine
	oauthMgr         *google.OAuthManager
	notificationMgr  *notifications.Manager
	auditLogger      *engine.AuditLogger
}

// NewHandler creates a new web handler.
func NewHandler(
	cfg *config.Config,
	sessionMgr *SessionManager,
	requestRepo *requests.Repository,
	apiKeyRepo *apikeys.Repository,
	tokenRepo *tokens.Repository,
	settingsStore *settings.Store,
	credentialsStore *notifications.CredentialsStore,
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
		config:           cfg,
		templates:        tmpl,
		sessionMgr:       sessionMgr,
		loginLimiter:     NewLoginLimiter(10, 10*time.Minute),
		settingsStore:    settingsStore,
		credentialsStore: credentialsStore,
		requestRepo:      requestRepo,
		apiKeyRepo:       apiKeyRepo,
		tokenRepo:        tokenRepo,
		engine:           eng,
		oauthMgr:         oauthMgr,
		notificationMgr:  notificationMgr,
		auditLogger:      auditLogger,
	}, nil
}

// loadTemplates loads all HTML templates.
// Each page is loaded separately with its own copy of the layout to avoid name collisions.
func loadTemplates(dir string) (*template.Template, error) {
	formatter := util.GetDefaultFormatter()
	funcMap := template.FuncMap{
		"formatTime": func(t time.Time) string {
			if formatter != nil {
				return formatter.FormatDateTime(t)
			}
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		"formatDate": func(t time.Time) string {
			if formatter != nil {
				return formatter.FormatDate(t)
			}
			return t.Format("Jan 2, 2006")
		},
		"formatJSON": func(v interface{}) string {
			data, _ := json.MarshalIndent(v, "", "  ")
			return string(data)
		},
		"statusClass": func(status string) string {
			switch status {
			case "pending_approval":
				return "bg-yellow-100 text-yellow-800"
			case "approved", "completed":
				return "bg-green-100 text-green-800"
			case "denied", "cancelled":
				return "bg-red-100 text-red-800"
			case "expired", "failed":
				return "bg-orange-100 text-orange-800"
			default:
				return "bg-blue-100 text-blue-800"
			}
		},
		"statusIcon": func(status string) string {
			switch status {
			case "pending_approval":
				return "PENDING"
			case "approved":
				return "APPROVED"
			case "completed":
				return "DONE"
			case "denied":
				return "DENIED"
			case "cancelled":
				return "CANCELLED"
			case "expired":
				return "EXPIRED"
			case "failed":
				return "FAILED"
			case "change_requested":
				return "CHANGE"
			default:
				return "UNKNOWN"
			}
		},
		"json": func(v interface{}) template.JS {
			data, _ := json.Marshal(v)
			return template.JS(data)
		},
	}

	// Create root template collection
	root := template.New("root").Funcs(funcMap)

	// Read the layout template
	layoutPath := filepath.Join(dir, "layout.html")
	layoutContent, err := os.ReadFile(layoutPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read layout: %w", err)
	}

	// List of page templates
	pageFiles := []string{
		"login.html", "dashboard.html", "pending.html", "detail.html",
		"history.html", "apikeys.html", "settings.html", "oauth.html",
		"oauth_not_configured.html", "setup.html", "setup_complete.html",
	}

	// Standalone approve page with its own minimal layout
	approveLayoutStr := `{{define "approve-layout"}}
<!DOCTYPE html>
<html lang="en" data-theme="system">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}} - SchedLock</title>
    <link rel="icon" type="image/svg+xml" href="/static/favicon.svg">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Newsreader:ital,opsz,wght@0,6..72,300;0,6..72,400;0,6..72,500;0,6..72,600;1,6..72,400&family=Inter:wght@400;450;500;600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <link rel="stylesheet" href="/static/css/styles.css">
</head>
<body>
    {{template "content-approve.html" .}}
    <script>
        (function() {
            const html = document.documentElement;
            const savedTheme = localStorage.getItem('schedlock-theme') || 'system';
            html.setAttribute('data-theme', savedTheme);
        })();
    </script>
</body>
</html>
{{end}}`
	if _, err := root.Parse(approveLayoutStr); err != nil {
		return nil, fmt.Errorf("failed to parse approve layout: %w", err)
	}

	// Load approve.html separately with content-approve.html naming
	approvePath := filepath.Join(dir, "approve.html")
	if approveContent, err := os.ReadFile(approvePath); err == nil {
		approveStr := string(approveContent)
		approveStr = strings.Replace(approveStr, `{{template "approve-layout" .}}`, "", 1)
		approveStr = strings.Replace(approveStr, `{{define "content"}}`, `{{define "content-approve.html"}}`, 1)
		if _, err := root.Parse(approveStr); err != nil {
			return nil, fmt.Errorf("failed to parse approve.html: %w", err)
		}
	}

	for _, page := range pageFiles {
		pagePath := filepath.Join(dir, page)
		pageContent, err := os.ReadFile(pagePath)
		if err != nil {
			continue // Skip missing templates
		}

		// Extract just the content between {{define "content"}} and {{end}}
		// and the {{template "layout" .}} call
		pageStr := string(pageContent)

		// Remove the {{template "layout" .}} line - we'll call the layout ourselves
		pageStr = strings.Replace(pageStr, `{{template "layout" .}}`, "", 1)

		// Rename the content block to be page-specific
		pageStr = strings.Replace(pageStr, `{{define "content"}}`, fmt.Sprintf(`{{define "content-%s"}}`, page), 1)

		// Create page-specific layout
		layoutStr := string(layoutContent)
		layoutStr = strings.Replace(layoutStr, `{{define "layout"}}`, fmt.Sprintf(`{{define "%s"}}`, page), 1)
		layoutStr = strings.Replace(layoutStr, `{{template "content" .}}`, fmt.Sprintf(`{{template "content-%s" .}}`, page), 1)

		// Parse both into root
		_, err = root.Parse(layoutStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse layout for %s: %w", page, err)
		}

		_, err = root.Parse(pageStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse page %s: %w", page, err)
		}
	}

	return root, nil
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
	var csrfToken string
	if session != nil && session.CSRFToken != "" {
		csrfToken = session.CSRFToken
	} else {
		csrfToken, _ = GenerateCSRFToken()
	}
	useTLS := strings.HasPrefix(h.config.Server.BaseURL, "https://")
	SetCSRFCookie(w, csrfToken, useTLS, h.sessionMgr.sessionDuration())
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

	h.render(w, r, "login.html", map[string]interface{}{
		"Title": "Sign In",
	})
}

// LoginSubmit handles login form submission.
func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	ip := clientIP(r)

	if h.loginLimiter != nil && !h.loginLimiter.Allow(ip) {
		h.render(w, r, "login.html", map[string]interface{}{
			"Title": "Sign In",
			"Error": "Too many login attempts. Please wait and try again.",
		})
		return
	}

	if !h.sessionMgr.VerifyPassword(password) {
		h.render(w, r, "login.html", map[string]interface{}{
			"Title": "Sign In",
			"Error": "Invalid password",
		})
		return
	}
	if h.loginLimiter != nil {
		h.loginLimiter.Reset(ip)
	}

	// Create session
	ipAddress := r.RemoteAddr
	userAgent := r.UserAgent()

	session, err := h.sessionMgr.CreateSession(r.Context(), "admin", ipAddress, userAgent)
	if err != nil {
		h.render(w, r, "login.html", map[string]interface{}{
			"Title": "Sign In",
			"Error": "Failed to create session",
		})
		return
	}

	useTLS := strings.HasPrefix(h.config.Server.BaseURL, "https://")
	SetSessionCookie(w, session.ID, useTLS, h.sessionMgr.sessionDuration())

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
	totalAPIKeys := 0
	for _, count := range apiKeyStats {
		totalAPIKeys += count
	}

	// Get pending requests
	pending, _ := h.requestRepo.GetPending(ctx)

	h.render(w, r, "dashboard.html", map[string]interface{}{
		"Title":           "Dashboard",
		"Stats":           stats,
		"APIKeyStats":     apiKeyStats,
		"APIKeyTotal":     totalAPIKeys,
		"PendingCount":    len(pending),
		"PendingRequests": pending,
	})
}

// PendingRequests shows pending approval requests.
func (h *Handler) PendingRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := h.requestRepo.GetPending(ctx)

	h.render(w, r, "pending.html", map[string]interface{}{
		"Title":    "Pending Approvals",
		"Requests": pending,
	})
}

// EventDisplayData holds parsed event data for human-readable display.
type EventDisplayData struct {
	Summary     string
	Description string
	Location    string
	CalendarID  string
	EventID     string // for update/delete
	Start       time.Time
	End         time.Time
	Attendees   []string
	IsAllDay    bool
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

	// Parse into human-readable format based on operation type
	eventData := h.parseEventPayload(req.Operation, req.Payload)

	h.render(w, r, "detail.html", map[string]interface{}{
		"Title":        "Request Details",
		"Request":      req,
		"Payload":      payload,
		"EventData":    eventData,
		"AuditEntries": auditEntries,
	})
}

// parseEventPayload extracts human-readable event data from the payload.
func (h *Handler) parseEventPayload(operation string, payload json.RawMessage) *EventDisplayData {
	data := &EventDisplayData{}

	switch operation {
	case "create_event":
		var intent struct {
			Summary     string    `json:"summary"`
			Description string    `json:"description"`
			Location    string    `json:"location"`
			CalendarID  string    `json:"calendarId"`
			Start       time.Time `json:"start"`
			End         time.Time `json:"end"`
			Attendees   []string  `json:"attendees"`
		}
		if err := json.Unmarshal(payload, &intent); err == nil {
			data.Summary = intent.Summary
			data.Description = intent.Description
			data.Location = intent.Location
			data.CalendarID = intent.CalendarID
			data.Start = intent.Start
			data.End = intent.End
			data.Attendees = intent.Attendees
		}

	case "update_event":
		var intent struct {
			EventID     string     `json:"eventId"`
			CalendarID  string     `json:"calendarId"`
			Summary     *string    `json:"summary"`
			Description *string    `json:"description"`
			Location    *string    `json:"location"`
			Start       *time.Time `json:"start"`
			End         *time.Time `json:"end"`
			Attendees   []string   `json:"attendees"`
		}
		if err := json.Unmarshal(payload, &intent); err == nil {
			data.EventID = intent.EventID
			data.CalendarID = intent.CalendarID
			if intent.Summary != nil {
				data.Summary = *intent.Summary
			}
			if intent.Description != nil {
				data.Description = *intent.Description
			}
			if intent.Location != nil {
				data.Location = *intent.Location
			}
			if intent.Start != nil {
				data.Start = *intent.Start
			}
			if intent.End != nil {
				data.End = *intent.End
			}
			data.Attendees = intent.Attendees
		}

	case "delete_event":
		var intent struct {
			EventID    string `json:"eventId"`
			CalendarID string `json:"calendarId"`
		}
		if err := json.Unmarshal(payload, &intent); err == nil {
			data.EventID = intent.EventID
			data.CalendarID = intent.CalendarID
		}
	}

	return data
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

// UpdatePayload handles updating the request payload before approval.
func (h *Handler) UpdatePayload(w http.ResponseWriter, r *http.Request) {
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

	// Only allow updates to pending requests
	if req.Status != "pending_approval" {
		http.Error(w, "Can only update pending requests", http.StatusBadRequest)
		return
	}

	// Build updated payload based on operation type
	var newPayload json.RawMessage

	switch req.Operation {
	case "create_event":
		// Parse form values and build new payload
		startStr := r.FormValue("start")
		endStr := r.FormValue("end")

		var startTime, endTime time.Time
		if startStr != "" {
			startTime, _ = time.Parse("2006-01-02T15:04", startStr)
		}
		if endStr != "" {
			endTime, _ = time.Parse("2006-01-02T15:04", endStr)
		}

		// Parse existing payload to keep non-editable fields
		var existing map[string]interface{}
		json.Unmarshal(req.Payload, &existing)

		// Update editable fields
		if summary := r.FormValue("summary"); summary != "" {
			existing["summary"] = summary
		}
		if description := r.FormValue("description"); description != "" {
			existing["description"] = description
		} else if r.FormValue("description_present") == "1" {
			existing["description"] = ""
		}
		if location := r.FormValue("location"); location != "" {
			existing["location"] = location
		} else if r.FormValue("location_present") == "1" {
			existing["location"] = ""
		}
		if !startTime.IsZero() {
			existing["start"] = startTime
		}
		if !endTime.IsZero() {
			existing["end"] = endTime
		}

		newPayload, _ = json.Marshal(existing)

	case "update_event":
		// Similar handling for update
		startStr := r.FormValue("start")
		endStr := r.FormValue("end")

		var existing map[string]interface{}
		json.Unmarshal(req.Payload, &existing)

		if summary := r.FormValue("summary"); summary != "" {
			existing["summary"] = summary
		}
		if description := r.FormValue("description"); description != "" {
			existing["description"] = description
		} else if r.FormValue("description_present") == "1" {
			existing["description"] = ""
		}
		if location := r.FormValue("location"); location != "" {
			existing["location"] = location
		} else if r.FormValue("location_present") == "1" {
			existing["location"] = ""
		}
		if startStr != "" {
			if t, err := time.Parse("2006-01-02T15:04", startStr); err == nil {
				existing["start"] = t
			}
		}
		if endStr != "" {
			if t, err := time.Parse("2006-01-02T15:04", endStr); err == nil {
				existing["end"] = t
			}
		}

		newPayload, _ = json.Marshal(existing)

	default:
		http.Error(w, "Cannot edit this request type", http.StatusBadRequest)
		return
	}

	// Update the payload in the database
	if err := h.requestRepo.UpdatePayload(ctx, requestID, newPayload); err != nil {
		http.Error(w, "Failed to update payload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Log the update
	session := GetSession(r.Context())
	actor := "web:admin"
	if session != nil {
		actor = "web:" + session.UserID
	}
	h.auditLogger.Log(ctx, "request_edited", requestID, req.APIKeyID, actor, nil)

	// Redirect back to the detail page
	http.Redirect(w, r, "/requests/"+requestID, http.StatusSeeOther)
}

// History shows audit log.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entries, _ := h.auditLogger.GetRecent(ctx, 100)

	h.render(w, r, "history.html", map[string]interface{}{
		"Title":   "Audit History",
		"Entries": entries,
	})
}

// APIKeys shows API key management.
func (h *Handler) APIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := h.apiKeyRepo.List(ctx, false)
	if err != nil {
		http.Error(w, "Failed to load API keys: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "apikeys.html", map[string]interface{}{
		"Title": "API Keys",
		"Keys":  keys,
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

	// If HTMX request, return the new key display with copy button
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<div class="alert alert-success">
			<p><strong>API Key Created!</strong></p>
			<p style="margin-bottom: var(--space-2);">Copy this key now - it won't be shown again:</p>
			<div style="display: flex; gap: var(--space-2); align-items: stretch;">
				<code id="new-api-key" class="key-display" style="flex: 1; margin: 0;">` + fullKey + `</code>
				<button type="button" onclick="copyApiKey()" class="btn btn-primary" style="white-space: nowrap;">
					<span id="copy-text">Copy</span>
				</button>
			</div>
			<p style="margin-top: var(--space-3); font-size: var(--text-sm); color: var(--text-secondary);">
				<a href="/apikeys" style="color: var(--accent);">Refresh page</a> to see the key in your list.
			</p>
		</div>
		<script>
		function copyApiKey() {
			const key = document.getElementById('new-api-key').textContent;
			navigator.clipboard.writeText(key).then(function() {
				document.getElementById('copy-text').textContent = 'Copied!';
				setTimeout(function() {
					document.getElementById('copy-text').textContent = 'Copy';
				}, 2000);
			});
		}
		</script>`))
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

// NotificationConfigView holds notification config for template rendering.
type NotificationConfigView struct {
	Enabled        bool
	Server         string
	Topic          string
	Token          string
	Priority       interface{} // string for ntfy, int for pushover
	Sound          string
	AppToken       string
	UserKey        string
	BotToken       string
	ChatID         string
	WebhookSecret  string
	URL            string // for generic webhook
	Secret         string // for generic webhook HMAC
	TimeoutSeconds int    // for generic webhook
}

// Settings shows settings page.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providers := h.notificationMgr.GetProviders()
	oauthConnected := h.oauthMgr.IsAuthenticated()
	updated := r.URL.Query().Get("updated") == "1"
	notificationsUpdated := r.URL.Query().Get("notifications_updated") == "1"
	oauthUpdated := r.URL.Query().Get("oauth_updated") == "1"

	// Load notification credentials from database
	ntfyConfig := NotificationConfigView{Server: "https://ntfy.sh", Priority: "high"}
	pushoverConfig := NotificationConfigView{Priority: 1, Sound: "pushover"}
	telegramConfig := NotificationConfigView{}
	webhookConfig := NotificationConfigView{TimeoutSeconds: 10}

	// Load Google OAuth credentials
	googleOAuthClientID := ""
	googleOAuthConfigured := false

	if h.credentialsStore != nil {
		if creds, _ := h.credentialsStore.Load(ctx, "ntfy"); creds != nil {
			ntfyConfig.Enabled = creds.Enabled
			if nc, ok := creds.Credentials.(*notifications.NtfyCredentials); ok && nc != nil {
				ntfyConfig.Server = nc.ServerURL
				ntfyConfig.Topic = nc.Topic
				ntfyConfig.Token = nc.Token
				ntfyConfig.Priority = nc.Priority
			}
		}
		if creds, _ := h.credentialsStore.Load(ctx, "pushover"); creds != nil {
			pushoverConfig.Enabled = creds.Enabled
			if pc, ok := creds.Credentials.(*notifications.PushoverCredentials); ok && pc != nil {
				pushoverConfig.AppToken = pc.AppToken
				pushoverConfig.UserKey = pc.UserKey
				pushoverConfig.Priority = pc.Priority
				pushoverConfig.Sound = pc.Sound
			}
		}
		if creds, _ := h.credentialsStore.Load(ctx, "telegram"); creds != nil {
			telegramConfig.Enabled = creds.Enabled
			if tc, ok := creds.Credentials.(*notifications.TelegramCredentials); ok && tc != nil {
				telegramConfig.BotToken = tc.BotToken
				telegramConfig.ChatID = tc.ChatID
				telegramConfig.WebhookSecret = tc.WebhookSecret
			}
		}
		if creds, _ := h.credentialsStore.Load(ctx, "webhook"); creds != nil {
			webhookConfig.Enabled = creds.Enabled
			if wc, ok := creds.Credentials.(*notifications.WebhookCredentials); ok && wc != nil {
				webhookConfig.URL = wc.URL
				webhookConfig.Secret = wc.Secret
				webhookConfig.TimeoutSeconds = wc.TimeoutSeconds
			}
		}
		// Load Google OAuth credentials
		if creds, _ := h.credentialsStore.Load(ctx, "google_oauth"); creds != nil {
			if gc, ok := creds.Credentials.(*notifications.GoogleOAuthCredentials); ok && gc != nil {
				googleOAuthClientID = gc.ClientID
				googleOAuthConfigured = gc.ClientID != ""
			}
		}
	}
	// Fall back to config for Google OAuth
	if !googleOAuthConfigured && h.config.Google.ClientID != "" {
		googleOAuthClientID = h.config.Google.ClientID
		googleOAuthConfigured = true
	}

	// Fall back to env config if no DB config
	if !ntfyConfig.Enabled && h.config.Notifications.Ntfy.Enabled {
		ntfyConfig.Enabled = true
		ntfyConfig.Server = h.config.Notifications.Ntfy.Server
		ntfyConfig.Topic = h.config.Notifications.Ntfy.Topic
		ntfyConfig.Token = h.config.Notifications.Ntfy.Token
		ntfyConfig.Priority = h.config.Notifications.Ntfy.Priority
	}
	if !pushoverConfig.Enabled && h.config.Notifications.Pushover.Enabled {
		pushoverConfig.Enabled = true
		pushoverConfig.AppToken = h.config.Notifications.Pushover.AppToken
		pushoverConfig.UserKey = h.config.Notifications.Pushover.UserKey
		pushoverConfig.Priority = h.config.Notifications.Pushover.Priority
		pushoverConfig.Sound = h.config.Notifications.Pushover.Sound
	}
	if !telegramConfig.Enabled && h.config.Notifications.Telegram.Enabled {
		telegramConfig.Enabled = true
		telegramConfig.BotToken = h.config.Notifications.Telegram.BotToken
		telegramConfig.ChatID = h.config.Notifications.Telegram.ChatID
		telegramConfig.WebhookSecret = h.config.Notifications.Telegram.WebhookSecret
	}
	if !webhookConfig.Enabled && h.config.Notifications.Webhook.Enabled {
		webhookConfig.Enabled = true
		webhookConfig.URL = h.config.Notifications.Webhook.URL
		webhookConfig.Secret = h.config.Notifications.Webhook.Secret
		webhookConfig.TimeoutSeconds = h.config.Notifications.Webhook.TimeoutSeconds
	}

	// Check if approval PIN is configured
	hasApprovalPIN := false
	if h.settingsStore != nil {
		hasApprovalPIN, _ = h.settingsStore.HasApprovalPIN(ctx)
	}

	h.render(w, r, "settings.html", map[string]interface{}{
		"Title":                 "Settings",
		"Providers":             providers,
		"OAuthConnected":        oauthConnected,
		"OAuthConfigured":       h.oauthMgr.IsConfigured(),
		"Config":                h.config,
		"Updated":               updated,
		"NotificationsUpdated":  notificationsUpdated,
		"OAuthUpdated":          oauthUpdated,
		"NtfyConfig":            ntfyConfig,
		"PushoverConfig":        pushoverConfig,
		"TelegramConfig":        telegramConfig,
		"WebhookConfig":         webhookConfig,
		"GoogleOAuthClientID":   googleOAuthClientID,
		"GoogleOAuthConfigured": googleOAuthConfigured,
		"HasApprovalPIN":        hasApprovalPIN,
	})
}

// SaveNotificationSettings handles notification provider configuration.
func (h *Handler) SaveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if h.credentialsStore == nil {
		http.Error(w, "credentials store unavailable", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// Save ntfy config
	ntfyEnabled := r.FormValue("ntfy_enabled") == "on"
	if ntfyEnabled {
		ntfyCreds := &notifications.NtfyCredentials{
			ServerURL:      strings.TrimSpace(r.FormValue("ntfy_server")),
			Topic:          strings.TrimSpace(r.FormValue("ntfy_topic")),
			Token:          strings.TrimSpace(r.FormValue("ntfy_token")),
			Priority:       strings.TrimSpace(r.FormValue("ntfy_priority")),
		}
		if ntfyCreds.ServerURL == "" {
			ntfyCreds.ServerURL = "https://ntfy.sh"
		}
		if ntfyCreds.Topic == "" {
			h.renderSettingsError(w, r, "ntfy topic is required when ntfy is enabled")
			return
		}
		if err := h.credentialsStore.Save(ctx, "ntfy", true, ntfyCreds); err != nil {
			h.renderSettingsError(w, r, "failed to save ntfy credentials")
			return
		}
	} else {
		h.credentialsStore.Save(ctx, "ntfy", false, &notifications.NtfyCredentials{})
	}

	// Save Pushover config
	pushoverEnabled := r.FormValue("pushover_enabled") == "on"
	if pushoverEnabled {
		priority, _ := strconv.Atoi(r.FormValue("pushover_priority"))
		pushoverCreds := &notifications.PushoverCredentials{
			AppToken: strings.TrimSpace(r.FormValue("pushover_app_token")),
			UserKey:  strings.TrimSpace(r.FormValue("pushover_user_key")),
			Priority: priority,
			Sound:    strings.TrimSpace(r.FormValue("pushover_sound")),
		}
		if pushoverCreds.AppToken == "" || pushoverCreds.UserKey == "" {
			h.renderSettingsError(w, r, "Pushover app token and user key are required")
			return
		}
		if err := h.credentialsStore.Save(ctx, "pushover", true, pushoverCreds); err != nil {
			h.renderSettingsError(w, r, "failed to save Pushover credentials")
			return
		}
	} else {
		h.credentialsStore.Save(ctx, "pushover", false, &notifications.PushoverCredentials{})
	}

	// Save Telegram config
	telegramEnabled := r.FormValue("telegram_enabled") == "on"
	if telegramEnabled {
		telegramCreds := &notifications.TelegramCredentials{
			BotToken:      strings.TrimSpace(r.FormValue("telegram_bot_token")),
			ChatID:        strings.TrimSpace(r.FormValue("telegram_chat_id")),
			WebhookSecret: strings.TrimSpace(r.FormValue("telegram_webhook_secret")),
		}
		if telegramCreds.BotToken == "" || telegramCreds.ChatID == "" {
			h.renderSettingsError(w, r, "Telegram bot token and chat ID are required")
			return
		}
		if err := h.credentialsStore.Save(ctx, "telegram", true, telegramCreds); err != nil {
			h.renderSettingsError(w, r, "failed to save Telegram credentials")
			return
		}
	} else {
		h.credentialsStore.Save(ctx, "telegram", false, &notifications.TelegramCredentials{})
	}

	// Save Webhook config
	webhookEnabled := r.FormValue("webhook_enabled") == "on"
	if webhookEnabled {
		timeout, _ := strconv.Atoi(r.FormValue("webhook_timeout"))
		if timeout <= 0 {
			timeout = 10
		}
		if timeout > 60 {
			timeout = 60
		}
		webhookCreds := &notifications.WebhookCredentials{
			URL:            strings.TrimSpace(r.FormValue("webhook_url")),
			Secret:         strings.TrimSpace(r.FormValue("webhook_secret")),
			TimeoutSeconds: timeout,
		}
		if webhookCreds.URL == "" {
			h.renderSettingsError(w, r, "Webhook URL is required when webhook is enabled")
			return
		}
		if err := h.credentialsStore.Save(ctx, "webhook", true, webhookCreds); err != nil {
			h.renderSettingsError(w, r, "failed to save Webhook credentials")
			return
		}
	} else {
		h.credentialsStore.Save(ctx, "webhook", false, &notifications.WebhookCredentials{})
	}

	// Audit log
	if h.auditLogger != nil {
		h.auditLogger.Log(ctx, database.AuditSettingsChanged, "", "", "web:admin", map[string]interface{}{
			"notification_settings_updated": true,
			"ntfy_enabled":                  ntfyEnabled,
			"pushover_enabled":              pushoverEnabled,
			"telegram_enabled":              telegramEnabled,
			"webhook_enabled":               webhookEnabled,
		})
	}

	http.Redirect(w, r, "/settings?notifications_updated=1", http.StatusSeeOther)
}

// SaveGoogleOAuthSettings saves Google OAuth credentials.
func (h *Handler) SaveGoogleOAuthSettings(w http.ResponseWriter, r *http.Request) {
	if h.credentialsStore == nil {
		http.Error(w, "credentials store unavailable", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	clientID := strings.TrimSpace(r.FormValue("google_client_id"))
	clientSecret := strings.TrimSpace(r.FormValue("google_client_secret"))

	// If secret empty and ID provided, keep existing secret
	if clientSecret == "" && clientID != "" {
		if existing, _ := h.credentialsStore.Load(ctx, "google_oauth"); existing != nil {
			if gc, ok := existing.Credentials.(*notifications.GoogleOAuthCredentials); ok && gc != nil {
				clientSecret = gc.ClientSecret
			}
		}
	}

	// Validation: both or neither
	if (clientID == "") != (clientSecret == "") {
		h.renderSettingsError(w, r, "Both Client ID and Client Secret are required")
		return
	}

	// Save to DB (encrypted)
	creds := &notifications.GoogleOAuthCredentials{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	if err := h.credentialsStore.Save(ctx, "google_oauth", clientID != "", creds); err != nil {
		h.renderSettingsError(w, r, "Failed to save credentials: "+err.Error())
		return
	}

	// Update OAuthManager immediately (no restart needed!)
	if clientID != "" {
		h.oauthMgr.UpdateCredentials(clientID, clientSecret)
	}

	// Audit log (don't log actual credentials)
	if h.auditLogger != nil {
		h.auditLogger.Log(ctx, database.AuditSettingsChanged, "", "", "web:admin",
			map[string]interface{}{"google_oauth_updated": true, "configured": clientID != ""})
	}

	http.Redirect(w, r, "/settings?oauth_updated=1", http.StatusSeeOther)
}

// SaveSettings handles runtime settings updates from the web UI.
func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if h.settingsStore == nil {
		http.Error(w, "settings store unavailable", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	approvalTimeout, err := parseIntField(r, "approval_timeout_minutes", h.config.Approval.TimeoutMinutes)
	if err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}
	retentionRequests, err := parseIntField(r, "retention_completed_days", h.config.Retention.CompletedRequestsDays)
	if err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}
	retentionAudit, err := parseIntField(r, "retention_audit_days", h.config.Retention.AuditLogDays)
	if err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}
	retentionWebhook, err := parseIntField(r, "retention_webhook_failures_days", h.config.Retention.WebhookFailuresDays)
	if err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}
	retentionEnabled := r.FormValue("retention_enabled") == "on"

	defaultAction := strings.TrimSpace(r.FormValue("approval_default_action"))
	if defaultAction == "" {
		defaultAction = h.config.Approval.DefaultAction
	}
	logLevel := strings.TrimSpace(r.FormValue("logging_level"))
	if logLevel == "" {
		logLevel = h.config.Logging.Level
	}
	logFormat := strings.TrimSpace(r.FormValue("logging_format"))
	if logFormat == "" {
		logFormat = h.config.Logging.Format
	}
	displayTimezone := strings.TrimSpace(r.FormValue("display_timezone"))
	if displayTimezone == "" {
		displayTimezone = h.config.Display.Timezone
	}
	displayDateFormat := strings.TrimSpace(r.FormValue("display_date_format"))
	if displayDateFormat == "" {
		displayDateFormat = h.config.Display.DateFormat
	}
	displayTimeFormat := strings.TrimSpace(r.FormValue("display_time_format"))
	if displayTimeFormat == "" {
		displayTimeFormat = h.config.Display.TimeFormat
	}
	displayDatetimeFormat := strings.TrimSpace(r.FormValue("display_datetime_format"))
	if displayDatetimeFormat == "" {
		displayDatetimeFormat = h.config.Display.DatetimeFormat
	}

	// Parse server base URL
	serverBaseURL := strings.TrimSpace(r.FormValue("server_base_url"))
	if serverBaseURL != "" {
		serverBaseURL = strings.TrimSuffix(serverBaseURL, "/") // Remove trailing slash
	}

	// Handle approval PIN
	clearPIN := r.FormValue("clear_pin") == "1"
	approvalPIN := strings.TrimSpace(r.FormValue("approval_pin"))

	if clearPIN {
		// Clear the PIN
		if err := h.settingsStore.SetApprovalPIN(ctx, ""); err != nil {
			h.renderSettingsError(w, r, "failed to clear PIN")
			return
		}
	} else if approvalPIN != "" {
		// Validate PIN format (4-8 digits)
		if len(approvalPIN) < 4 || len(approvalPIN) > 8 {
			h.renderSettingsError(w, r, "PIN must be 4-8 digits")
			return
		}
		for _, c := range approvalPIN {
			if c < '0' || c > '9' {
				h.renderSettingsError(w, r, "PIN must contain only digits")
				return
			}
		}
		// Set the new PIN
		if err := h.settingsStore.SetApprovalPIN(ctx, approvalPIN); err != nil {
			h.renderSettingsError(w, r, "failed to save PIN")
			return
		}
	}
	// If neither clear nor new PIN, keep existing

	settingsPayload := &settings.RuntimeSettings{
		Approval: &settings.ApprovalSettings{
			TimeoutMinutes: approvalTimeout,
			DefaultAction:  defaultAction,
		},
		Retention: &settings.RetentionSettings{
			Enabled:               &retentionEnabled,
			CompletedRequestsDays: retentionRequests,
			AuditLogDays:          retentionAudit,
			WebhookFailuresDays:   retentionWebhook,
		},
		Logging: &settings.LoggingSettings{
			Level:  logLevel,
			Format: logFormat,
		},
		Display: &settings.DisplaySettings{
			Timezone:       displayTimezone,
			DateFormat:     displayDateFormat,
			TimeFormat:     displayTimeFormat,
			DatetimeFormat: displayDatetimeFormat,
		},
		Server: &settings.ServerSettings{
			BaseURL: serverBaseURL,
		},
	}

	if err := settingsPayload.Validate(); err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}

	if err := h.settingsStore.Save(ctx, settingsPayload); err != nil {
		h.renderSettingsError(w, r, "failed to save settings")
		return
	}

	if err := settingsPayload.ApplyTo(h.config); err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}

	// Update OAuthManager if base URL changed
	if serverBaseURL != "" {
		h.oauthMgr.UpdateBaseURL(serverBaseURL)
	}

	util.SetDefaultLogger(util.NewLogger(h.config.Logging.Level, h.config.Logging.Format))
	formatter, err := util.NewDisplayFormatter(
		h.config.Display.Timezone,
		h.config.Display.DateFormat,
		h.config.Display.TimeFormat,
		h.config.Display.DatetimeFormat,
	)
	if err == nil {
		util.SetDefaultFormatter(formatter)
	}

	if h.auditLogger != nil {
		h.auditLogger.Log(ctx, database.AuditSettingsChanged, "", "", "web:admin", map[string]interface{}{
			"approval_timeout_minutes": approvalTimeout,
			"approval_default_action":  defaultAction,
			"retention_enabled":        retentionEnabled,
			"retention_completed_days": retentionRequests,
			"retention_audit_days":     retentionAudit,
			"retention_webhook_days":   retentionWebhook,
			"logging_level":            logLevel,
			"logging_format":           logFormat,
			"display_timezone":         displayTimezone,
			"display_date_format":      displayDateFormat,
			"display_time_format":      displayTimeFormat,
			"display_datetime_format":  displayDatetimeFormat,
			"server_base_url":          serverBaseURL,
		})
	}

	http.Redirect(w, r, "/settings?updated=1", http.StatusSeeOther)
}

func (h *Handler) renderSettingsError(w http.ResponseWriter, r *http.Request, message string) {
	providers := h.notificationMgr.GetProviders()
	oauthConnected := h.oauthMgr.IsAuthenticated()
	h.render(w, r, "settings.html", map[string]interface{}{
		"Title":          "Settings",
		"Providers":      providers,
		"OAuthConnected": oauthConnected,
		"Config":         h.config,
		"Error":          message,
	})
}

func parseIntField(r *http.Request, name string, fallback int) (int, error) {
	value := strings.TrimSpace(r.FormValue(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s", name)
	}
	return parsed, nil
}

// TestNotification tests a notification provider.
func (h *Handler) TestNotification(w http.ResponseWriter, r *http.Request) {
	provider := r.FormValue("provider")

	ctx := r.Context()
	w.Header().Set("Content-Type", "application/json")

	if err := h.notificationMgr.TestProvider(ctx, provider); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Test failed: " + err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Test notification sent successfully to " + provider,
	})
}

// OAuthStart initiates OAuth flow.
func (h *Handler) OAuthStart(w http.ResponseWriter, r *http.Request) {
	// Check if OAuth is configured
	if !h.oauthMgr.IsConfigured() {
		h.render(w, r, "oauth_not_configured.html", map[string]interface{}{
			"Title":   "Google Calendar Setup Required",
			"BaseURL": h.config.Server.BaseURL,
		})
		return
	}

	state, err := google.GenerateOAuthState()
	if err != nil {
		http.Error(w, "Failed to generate OAuth state", http.StatusInternalServerError)
		return
	}
	if err := h.oauthMgr.StoreOAuthState(r.Context(), state); err != nil {
		http.Error(w, "Failed to store OAuth state", http.StatusInternalServerError)
		return
	}

	authInfo := h.oauthMgr.GetAuthURLForHeadless(state)
	instructions := strings.Split(strings.TrimSpace(authInfo.Instructions), "\n")

	h.render(w, r, "oauth.html", map[string]interface{}{
		"Title":        "Connect Google Calendar",
		"AuthURL":      authInfo.AuthURL,
		"Instructions": instructions,
		"State":        state,
	})
}

// OAuthCallback handles OAuth callback.
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	if code == "" {
		code = r.URL.Query().Get("code")
	}
	state := r.FormValue("state")
	if state == "" {
		state = r.URL.Query().Get("state")
	}

	if code == "" {
		http.Error(w, "Authorization code required", http.StatusBadRequest)
		return
	}
	if state == "" {
		http.Error(w, "State parameter required", http.StatusBadRequest)
		return
	}

	if err := h.oauthMgr.ValidateOAuthState(r.Context(), state); err != nil {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
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

// PublicApprove handles the public approval page (GET shows form, POST processes action).
func (h *Handler) PublicApprove(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		h.renderApproveError(w, "Invalid Link", "No approval token provided.", false)
		return
	}

	ctx := r.Context()

	// Check if PIN is required
	requiresPIN := false
	if h.settingsStore != nil {
		requiresPIN, _ = h.settingsStore.HasApprovalPIN(ctx)
	}

	// Handle POST (approval/denial action)
	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		if action != "approve" && action != "deny" {
			h.renderApproveError(w, "Invalid Action", "Please use the approve or deny buttons.", false)
			return
		}

		// Validate PIN if required
		if requiresPIN {
			pin := r.FormValue("pin")
			valid, err := h.settingsStore.VerifyApprovalPIN(ctx, pin)
			if err != nil {
				h.renderApproveError(w, "Error", "Unable to verify PIN.", false)
				return
			}
			if !valid {
				// Re-show the form with error
				h.renderApproveWithPINError(w, ctx, token, "Incorrect PIN. Please try again.")
				return
			}
		}

		// Consume token and process
		requestID, err := h.tokenRepo.Consume(ctx, token, action)
		if err != nil {
			h.renderApproveError(w, "Link Expired or Used", err.Error(), false)
			return
		}

		// Process the approval/denial
		if err := h.engine.ProcessApproval(ctx, requestID, action, "link"); err != nil {
			h.renderApproveError(w, "Processing Failed", err.Error(), false)
			return
		}

		// Show success
		message := "The calendar request has been approved and will be processed."
		if action == "deny" {
			message = "The calendar request has been denied."
		}

		h.renderApprove(w, "approve-layout", map[string]interface{}{
			"Title":   "Request " + strings.Title(action) + "d",
			"Success": true,
			"Action":  action,
			"Message": message,
		})
		return
	}

	// GET - validate token and show approval form
	result, err := h.tokenRepo.Validate(ctx, token)
	if err != nil {
		h.renderApproveError(w, "Error", "Unable to validate approval link.", false)
		return
	}

	if !result.Valid {
		h.renderApproveError(w, "Link Expired or Used", result.Error, false)
		return
	}

	// Get request details
	req, err := h.requestRepo.GetByID(ctx, result.RequestID)
	if err != nil || req == nil {
		h.renderApproveError(w, "Request Not Found", "The associated request could not be found.", false)
		return
	}

	// Check if request is still pending
	if req.Status != "pending_approval" {
		h.renderApproveError(w, "Already Processed", "This request has already been "+req.Status+".", false)
		return
	}

	// Parse event details from payload
	eventDetails := extractEventDetails(req.Payload)

	// Calculate expires in
	expiresIn := formatDuration(time.Until(req.ExpiresAt))

	h.renderApprove(w, "approve-layout", map[string]interface{}{
		"Title":        "Approve Request",
		"Token":        token,
		"Request":      req,
		"EventDetails": eventDetails,
		"ExpiresIn":    expiresIn,
		"RequiresPIN":  requiresPIN,
	})
}

// renderApproveWithPINError re-renders the approval page with a PIN error message.
func (h *Handler) renderApproveWithPINError(w http.ResponseWriter, ctx context.Context, token, pinError string) {
	// Re-validate token to get request details
	result, err := h.tokenRepo.Validate(ctx, token)
	if err != nil || !result.Valid {
		h.renderApproveError(w, "Link Expired or Used", "The approval link is no longer valid.", false)
		return
	}

	req, err := h.requestRepo.GetByID(ctx, result.RequestID)
	if err != nil || req == nil {
		h.renderApproveError(w, "Request Not Found", "The associated request could not be found.", false)
		return
	}

	eventDetails := extractEventDetails(req.Payload)
	expiresIn := formatDuration(time.Until(req.ExpiresAt))

	h.renderApprove(w, "approve-layout", map[string]interface{}{
		"Title":        "Approve Request",
		"Token":        token,
		"Request":      req,
		"EventDetails": eventDetails,
		"ExpiresIn":    expiresIn,
		"RequiresPIN":  true,
		"PINError":     pinError,
	})
}

// EventDetails holds extracted event information for display.
type EventDetails struct {
	Title       string
	StartTime   string
	EndTime     string
	Location    string
	Description string
	Attendees   string
}

// extractEventDetails parses the request payload to extract event information.
func extractEventDetails(payload []byte) EventDetails {
	var details EventDetails
	var data map[string]interface{}

	if err := json.Unmarshal(payload, &data); err != nil {
		return details
	}

	// Try to get summary/title
	if v, ok := data["summary"].(string); ok {
		details.Title = v
	} else if v, ok := data["title"].(string); ok {
		details.Title = v
	}

	// Try to get start time
	if start, ok := data["start"].(map[string]interface{}); ok {
		if dt, ok := start["dateTime"].(string); ok {
			if t, err := time.Parse(time.RFC3339, dt); err == nil {
				formatter := util.GetDefaultFormatter()
				if formatter != nil {
					details.StartTime = formatter.FormatDateTime(t)
				} else {
					details.StartTime = t.Format("Mon Jan 2, 2006 3:04 PM")
				}
			}
		} else if d, ok := start["date"].(string); ok {
			details.StartTime = d + " (all day)"
		}
	}

	// Location
	if v, ok := data["location"].(string); ok {
		details.Location = v
	}

	// Description (truncate if long)
	if v, ok := data["description"].(string); ok {
		if len(v) > 200 {
			details.Description = v[:200] + "..."
		} else {
			details.Description = v
		}
	}

	// Attendees
	if attendees, ok := data["attendees"].([]interface{}); ok {
		var emails []string
		for _, a := range attendees {
			if att, ok := a.(map[string]interface{}); ok {
				if email, ok := att["email"].(string); ok {
					emails = append(emails, email)
				}
			}
		}
		if len(emails) > 0 {
			if len(emails) > 3 {
				details.Attendees = strings.Join(emails[:3], ", ") + fmt.Sprintf(" (+%d more)", len(emails)-3)
			} else {
				details.Attendees = strings.Join(emails, ", ")
			}
		}
	}

	return details
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	if d < time.Minute {
		return "less than a minute"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "in 1 minute"
		}
		return fmt.Sprintf("in %d minutes", mins)
	}
	hours := int(d.Hours())
	if hours == 1 {
		return "in 1 hour"
	}
	if hours < 24 {
		return fmt.Sprintf("in %d hours", hours)
	}
	days := hours / 24
	if days == 1 {
		return "in 1 day"
	}
	return fmt.Sprintf("in %d days", days)
}

// renderApprove renders the approval template.
func (h *Handler) renderApprove(w http.ResponseWriter, name string, data map[string]interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		util.Error("Template error", "template", name, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// renderApproveError renders an error on the approval page.
func (h *Handler) renderApproveError(w http.ResponseWriter, title, message string, showLogin bool) {
	h.renderApprove(w, "approve-layout", map[string]interface{}{
		"Title":         title,
		"Error":         message,
		"ErrorTitle":    title,
		"ShowLoginLink": showLogin,
	})
}
