package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
)

// TestEveryPageRenders executes each template with representative data.
//
// Parsing a template proves only that its syntax is valid. Type mismatches in
// comparisons, calls on absent fields, and missing helpers surface only when the
// template runs, so every page is rendered here with the data its handler
// supplies. Without this, a settings page that cannot render is discovered by
// whoever opens it.
func TestEveryPageRenders(t *testing.T) {
	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	cfg := &config.Config{}
	cfg.Display.Timezone = "UTC"
	cfg.Approval.TimeoutMinutes = 60
	cfg.Approval.DefaultAction = "deny"
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Retention.CompletedRequestsDays = 90
	cfg.Retention.AuditLogDays = 365
	cfg.Retention.WebhookFailuresDays = 30
	cfg.Server.BaseURL = "https://schedlock.example.com"

	session := &Session{ID: "sess_1", UserID: "admin", CSRFToken: "csrf-token"}

	request := RequestView{
		ID:        "req_abc123",
		Operation: database.OperationCreateEvent,
		Status:    database.StatusPendingApproval,
		Summary:   "Create: Quarterly review",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Payload:   json.RawMessage(`{"summary":"Quarterly review"}`),
		Details: EventSummary{
			Title:       "Quarterly review",
			StartTime:   "Mar 14, 2026 at 3:00 PM",
			EndTime:     "Mar 14, 2026 at 4:00 PM",
			Location:    "Room 4",
			Attendees:   "alice@example.com",
			Description: "Agenda:\n- numbers",
			Calendar:    "primary",
		},
		IsPending:  true,
		IsEditable: true,
	}

	providers := providerView{
		Enabled:  true,
		Server:   "https://ntfy.sh",
		Topic:    "schedlock",
		Priority: "high",
		Timeout:  10,
		HasToken: true,
	}

	base := pageData{
		"CSRFToken": "csrf-token",
		"BaseURL":   cfg.Server.BaseURL,
		"Config":    cfg,
		"Nav":       "dashboard",
		"Session":   session,
	}

	pages := map[string]pageData{
		"login.html": {
			"Title":    "Sign in",
			"Error":    "Incorrect password.",
			"Redirect": "/pending",
		},
		"dashboard.html": merge(base, pageData{
			"Title":       "Dashboard",
			"Stats":       &statsStub{TotalLastDay: 3},
			"APIKeyTotal": 2,
			"Pending":     []RequestView{request},
		}),
		"pending.html": merge(base, pageData{
			"Title":    "Pending approvals",
			"Requests": []RequestView{request},
		}),
		"detail.html": merge(base, pageData{
			"Title":   "Request details",
			"Request": request,
			"Form":    EventForm{Editable: true, Title: "Quarterly review", Start: time.Now(), End: time.Now().Add(time.Hour)},
			"History": []AuditView{{Timestamp: time.Now(), EventType: "request_created", Actor: "api"}},
			"Error":   "Something needs attention.",
		}),
		"history.html": merge(base, pageData{
			"Title": "Audit history",
			"Entries": []AuditView{
				{Timestamp: time.Now(), EventType: "login_success", Actor: "web:admin", IPAddress: "203.0.113.5"},
				{Timestamp: time.Now(), EventType: "request_created", RequestID: "req_abc123"},
			},
		}),
		"apikeys.html": merge(base, pageData{
			"Title":  "API keys",
			"Keys":   []database.APIKey{{ID: "key_1", Name: "Agent", Tier: "write", KeyPrefix: "sk_write_ab...cd", CreatedAt: time.Now()}},
			"NewKey": "sk_write_secretvalue",
			"Error":  "Something needs attention.",
		}),
		"settings.html": merge(base, pageData{
			"Title":            "Settings",
			"Ntfy":             providers,
			"Pushover":         providerView{Priority: "1", Sound: "pushover", HasAppKey: true},
			"Telegram":         providerView{ChatID: "12345", HasBotKey: true, HasSecret: true},
			"Webhook":          providerView{URL: "https://example.com/hook", Timeout: 10, HasSecret: true},
			"GoogleClientID":   "client.apps.googleusercontent.com",
			"GoogleConfigured": true,
			"GoogleConnected":  true,
			"RedirectURI":      cfg.Server.BaseURL + "/oauth/callback",
			"HasApprovalPIN":   true,
			"Saved":            "notifications",
			"Error":            "Something needs attention.",
		}),
		"oauth.html": merge(base, pageData{
			"Title":       "Connect Google Calendar",
			"AuthURL":     "https://accounts.google.com/o/oauth2/auth?x=1",
			"RedirectURI": cfg.Server.BaseURL + "/oauth/callback",
		}),
		"oauth_not_configured.html": merge(base, pageData{
			"Title":        "Google OAuth is not configured",
			"ErrorTitle":   "Google OAuth is not configured",
			"ErrorMessage": "Enter a client ID and secret in Settings.",
			"RedirectURI":  cfg.Server.BaseURL + "/oauth/callback",
		}),
		"setup.html": {
			"Title":       "Set up SchedLock",
			"BaseURL":     "https://schedlock.example.com",
			"RedirectURI": "https://schedlock.example.com/oauth/callback",
			"MinPassword": 12,
			"Error":       "The passwords do not match.",
			"CSRFToken":   "csrf-token",
		},
		"setup_complete.html": {
			"Title":     "Setup complete",
			"BaseURL":   "https://schedlock.example.com",
			"CSRFToken": "csrf-token",
		},
		"approve.html": {
			"Title":       "Approve request",
			"Token":       "dtok_example",
			"Request":     request,
			"Details":     request.Details,
			"ExpiresIn":   "47 minutes",
			"RequiresPIN": true,
			"PINError":    "Incorrect PIN.",
			"CSRFToken":   "csrf-token",
		},
	}

	for page, data := range pages {
		t.Run(page, func(t *testing.T) {
			rr := httptest.NewRecorder()
			templates.Render(rr, 200, page, data)

			if rr.Code != 200 {
				t.Fatalf("rendering %s returned status %d: %s", page, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if !strings.Contains(body, "</html>") {
				t.Errorf("%s produced no complete document", page)
			}
		})
	}
}

// TestApprovePageRendersEveryState covers the branches a recipient can land on.
func TestApprovePageRendersEveryState(t *testing.T) {
	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	states := map[string]pageData{
		"error":    {"Title": "Link expired", "ErrorTitle": "Link expired", "Error": "This approval link has expired."},
		"approved": {"Title": "Decision recorded", "Success": true, "Action": "approve", "Message": "Approved."},
		"denied":   {"Title": "Decision recorded", "Success": true, "Action": "deny", "Message": "Denied."},
	}

	for name, data := range states {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			templates.Render(rr, 200, "approve.html", data)
			if rr.Code != 200 {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestRenderedPagesEscapeUntrustedText checks that event content supplied by an
// agent cannot inject markup into the page an approver reads.
func TestRenderedPagesEscapeUntrustedText(t *testing.T) {
	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	const payload = `<script>alert('xss')</script>`

	request := RequestView{
		ID:        "req_abc123",
		Operation: database.OperationCreateEvent,
		Status:    database.StatusPendingApproval,
		Summary:   payload,
		ExpiresAt: time.Now().Add(time.Hour),
		Details: EventSummary{
			Title:       payload,
			Location:    payload,
			Description: payload,
			Attendees:   payload,
		},
		IsPending: true,
	}

	rr := httptest.NewRecorder()
	templates.Render(rr, 200, "approve.html", pageData{
		"Title":     "Approve request",
		"Token":     "dtok_example",
		"Request":   request,
		"Details":   request.Details,
		"ExpiresIn": "1 hour",
		"CSRFToken": "csrf-token",
	})

	body := rr.Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Error("an event title was rendered as live markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the injected markup to appear escaped")
	}
}

type statsStub struct {
	TotalLastDay int
	TotalPending int
}

func merge(base, extra pageData) pageData {
	out := make(pageData, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
