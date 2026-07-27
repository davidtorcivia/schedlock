package web

import (
	"net/http"
	"strings"

	"github.com/dtorcivia/schedlock/internal/config"
	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/util"
)

// minAdminPasswordLength is the shortest admin password accepted. This password
// guards every calendar operation the proxy can perform.
const minAdminPasswordLength = 12

// SetupHandler serves the first-run wizard.
//
// The wizard runs before any password exists, so it cannot be authenticated. It
// is available only while the instance is unconfigured, and completing it is
// what closes the window: the first person to reach a fresh instance claims it,
// so a new deployment should not be exposed publicly until setup is done.
type SetupHandler struct {
	config     *config.Config
	templates  *TemplateSet
	configPath string
	onComplete func()
}

// NewSetupHandler creates the setup wizard handler. onComplete is called after
// the configuration is written, so the process can restart into normal mode.
func NewSetupHandler(cfg *config.Config, configPath string, onComplete func()) (*SetupHandler, error) {
	templates, err := LoadTemplates()
	if err != nil {
		return nil, err
	}
	return &SetupHandler{
		config:     cfg,
		templates:  templates,
		configPath: configPath,
		onComplete: onComplete,
	}, nil
}

// RegisterRoutes registers the wizard's routes.
func (h *SetupHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", h.Setup)
	mux.HandleFunc("POST /setup", h.SetupSubmit)
	mux.Handle("GET /static/", StaticHandler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	})
}

// Setup shows the wizard.
func (h *SetupHandler) Setup(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, pageData{
		"Title":       "Set up SchedLock",
		"BaseURL":     h.config.Server.BaseURL,
		"RedirectURI": strings.TrimRight(h.config.Server.BaseURL, "/") + "/oauth/callback",
		"MinPassword": minAdminPasswordLength,
	})
}

// SetupSubmit stores the initial configuration.
func (h *SetupHandler) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")
	baseURL := strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/")
	clientID := strings.TrimSpace(r.FormValue("google_client_id"))
	clientSecret := strings.TrimSpace(r.FormValue("google_client_secret"))

	switch {
	case len(password) < minAdminPasswordLength:
		h.renderError(w, baseURL, "The password must be at least 12 characters.")
		return
	case password != confirm:
		h.renderError(w, baseURL, "The passwords do not match.")
		return
	case baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://"):
		h.renderError(w, baseURL, "The base URL must start with http:// or https://.")
		return
	case (clientID == "") != (clientSecret == ""):
		h.renderError(w, baseURL, "Enter both a Google client ID and secret, or leave both blank.")
		return
	}

	hash, err := schedcrypto.HashPassword(password)
	if err != nil {
		util.Error("Failed to hash the admin password", "error", err)
		h.renderError(w, baseURL, "The password could not be saved.")
		return
	}

	h.config.Auth.AdminPasswordHash = hash
	if baseURL != "" {
		h.config.Server.BaseURL = baseURL
		h.config.Google.RedirectURI = baseURL + "/oauth/callback"
	}
	if clientID != "" {
		h.config.Google.ClientID = clientID
		h.config.Google.ClientSecret = clientSecret
	}

	if err := config.SaveConfigFile(h.config, h.configPath); err != nil {
		util.Error("Failed to write the configuration file", "error", err, "path", h.configPath)
		h.renderError(w, baseURL, "The configuration could not be written. Check that the data directory is writable.")
		return
	}

	util.Info("Setup complete", "config_path", h.configPath)

	h.render(w, http.StatusOK, pageData{
		"Title":   "Setup complete",
		"BaseURL": h.config.Server.BaseURL,
	})

	// Restarting is what switches the process out of setup mode and into the
	// configured application.
	if h.onComplete != nil {
		go h.onComplete()
	}
}

func (h *SetupHandler) render(w http.ResponseWriter, status int, data pageData) {
	page := "setup.html"
	if data["Title"] == "Setup complete" {
		page = "setup_complete.html"
	}
	h.templates.Render(w, status, page, data)
}

func (h *SetupHandler) renderError(w http.ResponseWriter, baseURL, message string) {
	h.render(w, http.StatusBadRequest, pageData{
		"Title":       "Set up SchedLock",
		"Error":       message,
		"BaseURL":     baseURL,
		"RedirectURI": strings.TrimRight(baseURL, "/") + "/oauth/callback",
		"MinPassword": minAdminPasswordLength,
	})
}
