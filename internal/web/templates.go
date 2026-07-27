package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// pageLayouts maps each page to the layout it renders inside.
//
// Every page is parsed into its own template set, so each may define "content"
// under its own name without colliding. This replaces the previous approach of
// rewriting template source with string substitution before parsing, which made
// the naming scheme invisible to anyone reading the templates.
var pageLayouts = map[string]string{
	"login.html":                "layout.html",
	"dashboard.html":            "layout.html",
	"pending.html":              "layout.html",
	"detail.html":               "layout.html",
	"history.html":              "layout.html",
	"apikeys.html":              "layout.html",
	"settings.html":             "layout.html",
	"oauth.html":                "layout.html",
	"oauth_not_configured.html": "layout.html",
	"setup.html":                "layout.html",
	"setup_complete.html":       "layout.html",
	"approve.html":              "approve_layout.html",
}

// TemplateSet holds the parsed templates for every page.
type TemplateSet struct {
	pages map[string]*template.Template
}

// LoadTemplates parses the embedded templates.
func LoadTemplates() (*TemplateSet, error) {
	set := &TemplateSet{pages: make(map[string]*template.Template, len(pageLayouts))}

	for page, layout := range pageLayouts {
		tmpl, err := template.New(page).Funcs(templateFuncs()).ParseFS(templateFS,
			"assets/templates/"+layout,
			"assets/templates/"+page,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", page, err)
		}
		set.pages[page] = tmpl
	}

	return set, nil
}

// Render writes a page to the response.
//
// The page is rendered into a buffer first: a template that fails halfway
// through would otherwise leave a half-written page with a 200 status already
// committed, and the reader would see a truncated approval screen.
func (s *TemplateSet) Render(w http.ResponseWriter, status int, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		util.Error("Unknown template requested", "template", page)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		util.Error("Template execution failed", "template", page, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		util.Debug("Failed to write response body", "template", page, "error", err)
	}
}

// templateFuncs are the helpers available to every template.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// Timestamps are formatted through the current display formatter, which
		// is looked up per call so a timezone change in settings takes effect
		// without restarting or reloading templates.
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return util.GetDefaultFormatter().FormatDateTime(t)
		},
		"formatDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return util.GetDefaultFormatter().FormatDate(t)
		},
		"formatForInput": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return util.GetDefaultFormatter().FormatForInput(t)
		},
		"relativeTo": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return util.GetDefaultFormatter().FormatExpiresIn(t)
		},
		"prettyJSON": prettyJSON,
		"operationLabel": func(operation string) string {
			switch operation {
			case "create_event":
				return "Create event"
			case "update_event":
				return "Update event"
			case "delete_event":
				return "Delete event"
			default:
				return operation
			}
		},
		"statusLabel": func(status string) string {
			switch status {
			case "pending_approval":
				return "Pending approval"
			case "change_requested":
				return "Changes requested"
			default:
				return capitalize(status)
			}
		},
		"statusClass": func(status string) string {
			switch status {
			case "pending_approval", "approved", "executing":
				return "badge-primary"
			case "completed":
				return "badge-success"
			case "denied", "failed":
				return "badge-error"
			case "expired", "cancelled", "change_requested":
				return "badge-warning"
			default:
				return "badge-default"
			}
		},
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}
