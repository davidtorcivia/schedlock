package web

import (
	"html/template"
	"net/http"

	"github.com/dtorcivia/schedlock/internal/config"
	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
)

// SetupHandler handles the first-run setup wizard.
type SetupHandler struct {
	config     *config.Config
	templates  *template.Template
	configPath string
}

// NewSetupHandler creates a new setup handler.
func NewSetupHandler(cfg *config.Config, configPath string) (*SetupHandler, error) {
	tmpl, err := loadTemplates("web/templates")
	if err != nil {
		return nil, err
	}
	return &SetupHandler{
		config:     cfg,
		templates:  tmpl,
		configPath: configPath,
	}, nil
}

// Setup displays the setup wizard.
func (h *SetupHandler) Setup(w http.ResponseWriter, r *http.Request) {
	h.render(w, "setup.html", map[string]interface{}{
		"Title":   "Initial Setup",
		"BaseURL": h.config.Server.BaseURL,
	})
}

// SetupSubmit handles the setup form submission.
func (h *SetupHandler) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")
	baseURL := r.FormValue("base_url")
	googleClientID := r.FormValue("google_client_id")
	googleClientSecret := r.FormValue("google_client_secret")

	// Validation
	if password == "" {
		h.renderError(w, "Password is required")
		return
	}
	if len(password) < 8 {
		h.renderError(w, "Password must be at least 8 characters")
		return
	}
	if password != confirmPassword {
		h.renderError(w, "Passwords do not match")
		return
	}

	// Hash password
	hash, err := schedcrypto.HashPassword(password)
	if err != nil {
		h.renderError(w, "Failed to hash password: "+err.Error())
		return
	}

	// Update config
	h.config.Auth.AdminPasswordHash = hash
	if baseURL != "" {
		h.config.Server.BaseURL = baseURL
		h.config.Google.RedirectURI = baseURL + "/oauth/callback"
	}
	if googleClientID != "" {
		h.config.Google.ClientID = googleClientID
	}
	if googleClientSecret != "" {
		h.config.Google.ClientSecret = googleClientSecret
	}

	// Save config file
	if err := config.SaveConfigFile(h.config, h.configPath); err != nil {
		h.renderError(w, "Failed to save configuration: "+err.Error())
		return
	}

	// Render success page with restart instructions
	h.render(w, "setup_complete.html", map[string]interface{}{
		"Title": "Setup Complete",
	})
}

// RegisterRoutes registers setup wizard routes.
func (h *SetupHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", h.Setup)
	mux.HandleFunc("POST /setup", h.SetupSubmit)

	// Redirect all other routes to setup
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/setup" {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/setup", http.StatusFound)
	})
}

func (h *SetupHandler) render(w http.ResponseWriter, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *SetupHandler) renderError(w http.ResponseWriter, msg string) {
	h.render(w, "setup.html", map[string]interface{}{
		"Title":   "Initial Setup",
		"Error":   msg,
		"BaseURL": h.config.Server.BaseURL,
	})
}
