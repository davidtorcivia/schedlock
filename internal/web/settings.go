package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/util"
)

// providerView describes a provider's configuration for the settings page.
//
// Secrets are represented by a mask and a "configured" flag; their values never
// leave the server once stored.
type providerView struct {
	Enabled    bool
	Server     string
	Topic      string
	HasToken   bool
	TokenHint  string
	Priority   string
	AppToken   string
	HasAppKey  bool
	UserKey    string
	HasUserKey bool
	Sound      string
	ChatID     string
	HasBotKey  bool
	HasSecret  bool
	SecretHint string
	URL        string
	Timeout    int
}

// Settings renders the settings page.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	h.renderSettings(w, r, http.StatusOK, "", r.URL.Query().Get("saved"))
}

// renderSettings builds and renders the settings page in one place, so an
// error re-render shows the same fully populated form as a fresh load.
func (h *Handler) renderSettings(w http.ResponseWriter, r *http.Request, status int, errMessage, saved string) {
	ctx := r.Context()

	stored := map[string]*notifications.ProviderCredentials{}
	if h.CredentialsStore != nil {
		loaded, err := h.CredentialsStore.LoadAll(ctx)
		if err != nil {
			util.Error("Failed to load notification credentials", "error", err)
		} else {
			stored = loaded
		}
	}

	hasPIN, err := h.SettingsStore.HasApprovalPIN(ctx)
	if err != nil {
		util.Error("Failed to read approval PIN setting", "error", err)
	}

	googleClientID, googleConfigured := h.googleCredentialSummary(ctx, stored)

	data := pageData{
		"Title":            "Settings",
		"Nav":              "settings",
		"Config":           h.Config,
		"Ntfy":             h.ntfyView(stored),
		"Pushover":         h.pushoverView(stored),
		"Telegram":         h.telegramView(stored),
		"Webhook":          h.webhookView(stored),
		"GoogleClientID":   googleClientID,
		"GoogleConfigured": googleConfigured,
		"GoogleConnected":  h.OAuthManager.HasToken(ctx),
		"RedirectURI":      h.OAuthManager.RedirectURI(),
		"HasApprovalPIN":   hasPIN,
		"Saved":            saved,
	}
	if errMessage != "" {
		data["Error"] = errMessage
	}

	h.renderStatus(w, r, status, "settings.html", data)
}

func (h *Handler) ntfyView(stored map[string]*notifications.ProviderCredentials) providerView {
	view := providerView{Server: "https://ntfy.sh", Priority: "high"}

	if creds, ok := stored[notifications.ProviderNtfy]; ok {
		view.Enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.NtfyCredentials); ok && c != nil {
			if c.ServerURL != "" {
				view.Server = c.ServerURL
			}
			view.Topic = c.Topic
			view.HasToken = c.Token != ""
			view.TokenHint = maskSecret(c.Token)
			if c.Priority != "" {
				view.Priority = c.Priority
			}
			return view
		}
	}

	// Fall back to file/environment configuration when nothing is stored.
	cfg := h.Config.Notifications.Ntfy
	view.Enabled = cfg.Enabled
	if cfg.Server != "" {
		view.Server = cfg.Server
	}
	view.Topic = cfg.Topic
	view.HasToken = cfg.Token != ""
	view.TokenHint = maskSecret(cfg.Token)
	if cfg.Priority != "" {
		view.Priority = cfg.Priority
	}
	return view
}

func (h *Handler) pushoverView(stored map[string]*notifications.ProviderCredentials) providerView {
	view := providerView{Priority: "1", Sound: "pushover"}

	if creds, ok := stored[notifications.ProviderPushover]; ok {
		view.Enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.PushoverCredentials); ok && c != nil {
			view.HasAppKey = c.AppToken != ""
			view.HasUserKey = c.UserKey != ""
			view.Priority = strconv.Itoa(c.Priority)
			if c.Sound != "" {
				view.Sound = c.Sound
			}
			return view
		}
	}

	cfg := h.Config.Notifications.Pushover
	view.Enabled = cfg.Enabled
	view.HasAppKey = cfg.AppToken != ""
	view.HasUserKey = cfg.UserKey != ""
	view.Priority = strconv.Itoa(cfg.Priority)
	if cfg.Sound != "" {
		view.Sound = cfg.Sound
	}
	return view
}

func (h *Handler) telegramView(stored map[string]*notifications.ProviderCredentials) providerView {
	var view providerView

	if creds, ok := stored[notifications.ProviderTelegram]; ok {
		view.Enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.TelegramCredentials); ok && c != nil {
			view.HasBotKey = c.BotToken != ""
			view.ChatID = c.ChatID
			view.HasSecret = c.WebhookSecret != ""
			return view
		}
	}

	cfg := h.Config.Notifications.Telegram
	view.Enabled = cfg.Enabled
	view.HasBotKey = cfg.BotToken != ""
	view.ChatID = cfg.ChatID
	view.HasSecret = cfg.WebhookSecret != ""
	return view
}

func (h *Handler) webhookView(stored map[string]*notifications.ProviderCredentials) providerView {
	view := providerView{Timeout: 10}

	if creds, ok := stored[notifications.ProviderWebhook]; ok {
		view.Enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.WebhookCredentials); ok && c != nil {
			view.URL = c.URL
			view.HasSecret = c.Secret != ""
			if c.TimeoutSeconds > 0 {
				view.Timeout = c.TimeoutSeconds
			}
			return view
		}
	}

	cfg := h.Config.Notifications.Webhook
	view.Enabled = cfg.Enabled
	view.URL = cfg.URL
	view.HasSecret = cfg.Secret != ""
	if cfg.TimeoutSeconds > 0 {
		view.Timeout = cfg.TimeoutSeconds
	}
	return view
}

func (h *Handler) googleCredentialSummary(ctx context.Context, stored map[string]*notifications.ProviderCredentials) (string, bool) {
	if creds, ok := stored[notifications.ProviderGoogleOAuth]; ok {
		if c, ok := creds.Credentials.(*notifications.GoogleOAuthCredentials); ok && c != nil && c.ClientID != "" {
			return c.ClientID, true
		}
	}
	if h.Config.Google.ClientID != "" {
		return h.Config.Google.ClientID, true
	}
	return "", h.OAuthManager.IsConfigured(ctx)
}

// SaveNotificationSettings stores notification provider configuration.
//
// A secret left blank keeps whatever is already stored, so the operator can
// change a topic or chat ID without re-entering credentials the page never
// showed them.
func (h *Handler) SaveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.saveNtfy(ctx, r); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	if err := h.savePushover(ctx, r); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	if err := h.saveTelegram(ctx, r); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	if err := h.saveWebhook(ctx, r); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	// Push the new configuration into the live providers so the change takes
	// effect without a restart.
	if err := h.NotificationMgr.Reload(ctx); err != nil {
		util.Error("Failed to apply notification settings", "error", err)
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditSettingsChanged,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
		Details:   map[string]any{"section": "notifications"},
	})

	http.Redirect(w, r, "/settings?saved=notifications", http.StatusSeeOther)
}

func (h *Handler) saveNtfy(ctx context.Context, r *http.Request) error {
	enabled := r.FormValue("ntfy_enabled") == "on"

	existing := &notifications.NtfyCredentials{}
	if creds, _ := h.CredentialsStore.Load(ctx, notifications.ProviderNtfy); creds != nil {
		if c, ok := creds.Credentials.(*notifications.NtfyCredentials); ok && c != nil {
			existing = c
		}
	}

	creds := &notifications.NtfyCredentials{
		ServerURL: strings.TrimSpace(r.FormValue("ntfy_server")),
		Topic:     strings.TrimSpace(r.FormValue("ntfy_topic")),
		Token:     keepOrReplace(existing.Token, r.FormValue("ntfy_token")),
		Priority:  strings.TrimSpace(r.FormValue("ntfy_priority")),
	}
	if creds.ServerURL == "" {
		creds.ServerURL = "https://ntfy.sh"
	}
	if enabled && creds.Topic == "" {
		return errValidation("An ntfy topic is required when ntfy is enabled.")
	}

	return h.CredentialsStore.Save(ctx, notifications.ProviderNtfy, enabled, creds)
}

func (h *Handler) savePushover(ctx context.Context, r *http.Request) error {
	enabled := r.FormValue("pushover_enabled") == "on"

	existing := &notifications.PushoverCredentials{}
	if creds, _ := h.CredentialsStore.Load(ctx, notifications.ProviderPushover); creds != nil {
		if c, ok := creds.Credentials.(*notifications.PushoverCredentials); ok && c != nil {
			existing = c
		}
	}

	priority, err := strconv.Atoi(strings.TrimSpace(r.FormValue("pushover_priority")))
	if err != nil {
		priority = existing.Priority
	}

	creds := &notifications.PushoverCredentials{
		AppToken: keepOrReplace(existing.AppToken, r.FormValue("pushover_app_token")),
		UserKey:  keepOrReplace(existing.UserKey, r.FormValue("pushover_user_key")),
		Priority: priority,
		Sound:    strings.TrimSpace(r.FormValue("pushover_sound")),
	}
	if enabled && (creds.AppToken == "" || creds.UserKey == "") {
		return errValidation("A Pushover application token and user key are required when Pushover is enabled.")
	}

	return h.CredentialsStore.Save(ctx, notifications.ProviderPushover, enabled, creds)
}

func (h *Handler) saveTelegram(ctx context.Context, r *http.Request) error {
	enabled := r.FormValue("telegram_enabled") == "on"

	existing := &notifications.TelegramCredentials{}
	if creds, _ := h.CredentialsStore.Load(ctx, notifications.ProviderTelegram); creds != nil {
		if c, ok := creds.Credentials.(*notifications.TelegramCredentials); ok && c != nil {
			existing = c
		}
	}

	creds := &notifications.TelegramCredentials{
		BotToken:      keepOrReplace(existing.BotToken, r.FormValue("telegram_bot_token")),
		ChatID:        strings.TrimSpace(r.FormValue("telegram_chat_id")),
		WebhookSecret: keepOrReplace(existing.WebhookSecret, r.FormValue("telegram_webhook_secret")),
	}
	if enabled && (creds.BotToken == "" || creds.ChatID == "") {
		return errValidation("A Telegram bot token and chat ID are required when Telegram is enabled.")
	}

	// The webhook endpoint authenticates incoming updates with this secret and
	// refuses to serve without one, so a usable secret is generated rather than
	// leaving the callback path closed.
	if enabled && creds.WebhookSecret == "" {
		secret, err := generateWebhookSecret()
		if err != nil {
			return err
		}
		creds.WebhookSecret = secret
		util.Info("Generated a Telegram webhook secret")
	}

	return h.CredentialsStore.Save(ctx, notifications.ProviderTelegram, enabled, creds)
}

func (h *Handler) saveWebhook(ctx context.Context, r *http.Request) error {
	enabled := r.FormValue("webhook_enabled") == "on"

	existing := &notifications.WebhookCredentials{}
	if creds, _ := h.CredentialsStore.Load(ctx, notifications.ProviderWebhook); creds != nil {
		if c, ok := creds.Credentials.(*notifications.WebhookCredentials); ok && c != nil {
			existing = c
		}
	}

	timeout, err := strconv.Atoi(strings.TrimSpace(r.FormValue("webhook_timeout")))
	if err != nil || timeout < 1 {
		timeout = 10
	}
	if timeout > 60 {
		timeout = 60
	}

	creds := &notifications.WebhookCredentials{
		URL:            strings.TrimSpace(r.FormValue("webhook_url")),
		Secret:         keepOrReplace(existing.Secret, r.FormValue("webhook_secret")),
		TimeoutSeconds: timeout,
	}
	if enabled {
		if creds.URL == "" {
			return errValidation("A webhook URL is required when webhook notifications are enabled.")
		}
		if !strings.HasPrefix(creds.URL, "http://") && !strings.HasPrefix(creds.URL, "https://") {
			return errValidation("The webhook URL must start with http:// or https://.")
		}
	}

	return h.CredentialsStore.Save(ctx, notifications.ProviderWebhook, enabled, creds)
}

// SaveGoogleOAuthSettings stores the Google OAuth client credentials.
func (h *Handler) SaveGoogleOAuthSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	clientID := strings.TrimSpace(r.FormValue("google_client_id"))
	clientSecret := strings.TrimSpace(r.FormValue("google_client_secret"))

	existing := &notifications.GoogleOAuthCredentials{}
	if creds, _ := h.CredentialsStore.Load(ctx, notifications.ProviderGoogleOAuth); creds != nil {
		if c, ok := creds.Credentials.(*notifications.GoogleOAuthCredentials); ok && c != nil {
			existing = c
		}
	}
	clientSecret = keepOrReplace(existing.ClientSecret, clientSecret)

	if (clientID == "") != (clientSecret == "") {
		h.renderSettings(w, r, http.StatusBadRequest,
			"Both a client ID and a client secret are required.", "")
		return
	}

	creds := &notifications.GoogleOAuthCredentials{ClientID: clientID, ClientSecret: clientSecret}
	if err := h.CredentialsStore.Save(ctx, notifications.ProviderGoogleOAuth, clientID != "", creds); err != nil {
		util.Error("Failed to save Google OAuth credentials", "error", err)
		h.renderSettings(w, r, http.StatusInternalServerError,
			"The credentials could not be saved.", "")
		return
	}

	if clientID != "" {
		h.OAuthManager.UpdateCredentials(clientID, clientSecret)
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditSettingsChanged,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
		Details:   map[string]any{"section": "google_oauth", "configured": clientID != ""},
	})

	http.Redirect(w, r, "/settings?saved=google", http.StatusSeeOther)
}

// SaveSettings stores the runtime settings.
func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Start from what is stored so that fields this form does not own (the
	// approval PIN in particular) survive the write. Rebuilding the settings
	// document from the form alone silently discarded the PIN on every save.
	current, err := h.SettingsStore.Load(ctx)
	if err != nil {
		util.Error("Failed to load runtime settings", "error", err)
		h.renderSettings(w, r, http.StatusInternalServerError, "Settings could not be loaded.", "")
		return
	}

	approvalTimeout, err := formInt(r, "approval_timeout_minutes", h.Config.Approval.TimeoutMinutes)
	if err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	retentionRequests, err := formInt(r, "retention_completed_days", h.Config.Retention.CompletedRequestsDays)
	if err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	retentionAudit, err := formInt(r, "retention_audit_days", h.Config.Retention.AuditLogDays)
	if err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	retentionWebhook, err := formInt(r, "retention_webhook_failures_days", h.Config.Retention.WebhookFailuresDays)
	if err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	retentionEnabled := r.FormValue("retention_enabled") == "on"

	current.Approval = &settings.ApprovalSettings{
		TimeoutMinutes: approvalTimeout,
		DefaultAction:  formValueOr(r, "approval_default_action", h.Config.Approval.DefaultAction),
	}
	current.Retention = &settings.RetentionSettings{
		Enabled:               &retentionEnabled,
		CompletedRequestsDays: retentionRequests,
		AuditLogDays:          retentionAudit,
		WebhookFailuresDays:   retentionWebhook,
	}
	current.Logging = &settings.LoggingSettings{
		Level:  formValueOr(r, "logging_level", h.Config.Logging.Level),
		Format: formValueOr(r, "logging_format", h.Config.Logging.Format),
	}
	current.Display = &settings.DisplaySettings{
		Timezone:       formValueOr(r, "display_timezone", h.Config.Display.Timezone),
		DateFormat:     formValueOr(r, "display_date_format", h.Config.Display.DateFormat),
		TimeFormat:     formValueOr(r, "display_time_format", h.Config.Display.TimeFormat),
		DatetimeFormat: formValueOr(r, "display_datetime_format", h.Config.Display.DatetimeFormat),
	}
	current.Server = &settings.ServerSettings{
		BaseURL: strings.TrimRight(strings.TrimSpace(r.FormValue("server_base_url")), "/"),
	}

	if err := h.applyApprovalPIN(ctx, r, current); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	if err := current.Validate(); err != nil {
		h.renderSettings(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	if err := h.SettingsStore.Save(ctx, current); err != nil {
		util.Error("Failed to save runtime settings", "error", err)
		h.renderSettings(w, r, http.StatusInternalServerError, "Settings could not be saved.", "")
		return
	}

	if err := current.ApplyTo(h.Config); err != nil {
		util.Error("Failed to apply runtime settings", "error", err)
	}
	h.applyRuntimeSettings()

	if current.Server.BaseURL != "" {
		h.OAuthManager.UpdateBaseURL(current.Server.BaseURL)
	}

	h.AuditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditSettingsChanged,
		Actor:     h.actor(r),
		IPAddress: h.ClientIP(r),
		Details: map[string]any{
			"section":                  "general",
			"approval_timeout_minutes": approvalTimeout,
			"retention_enabled":        retentionEnabled,
			"display_timezone":         current.Display.Timezone,
		},
	})

	http.Redirect(w, r, "/settings?saved=general", http.StatusSeeOther)
}

// applyApprovalPIN sets or clears the approval PIN on the settings document.
func (h *Handler) applyApprovalPIN(ctx context.Context, r *http.Request, current *settings.RuntimeSettings) error {
	if r.FormValue("clear_pin") == "1" {
		current.ClearApprovalPIN()
		return nil
	}

	pin := strings.TrimSpace(r.FormValue("approval_pin"))
	if pin == "" {
		return nil // Leave the existing PIN untouched.
	}

	if err := current.SetApprovalPIN(pin); err != nil {
		return err
	}

	util.Info("Approval PIN updated")
	_ = ctx
	return nil
}

// applyRuntimeSettings rebuilds the logger and display formatter after a
// settings change, so the new values take effect immediately.
func (h *Handler) applyRuntimeSettings() {
	util.SetDefaultLogger(util.NewLogger(h.Config.Logging.Level, h.Config.Logging.Format))

	formatter, err := util.NewDisplayFormatter(
		h.Config.Display.Timezone,
		h.Config.Display.DateFormat,
		h.Config.Display.TimeFormat,
		h.Config.Display.DatetimeFormat,
	)
	if err != nil {
		util.Error("Failed to apply display settings", "error", err)
		return
	}
	util.SetDefaultFormatter(formatter)
}

// TestNotification sends a test notification through one provider.
func (h *Handler) TestNotification(w http.ResponseWriter, r *http.Request) {
	provider := r.FormValue("provider")

	if err := h.NotificationMgr.TestProvider(r.Context(), provider); err != nil {
		util.Warn("Test notification failed", "provider", provider, "error", err)
		response.JSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Test failed: " + util.TruncateString(err.Error(), 200),
		})
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Test notification sent through " + provider,
	})
}

// keepOrReplace returns the submitted value, or the stored one when the field
// was left blank.
func keepOrReplace(existing, submitted string) string {
	submitted = strings.TrimSpace(submitted)
	if submitted == "" {
		return existing
	}
	return submitted
}

// generateWebhookSecret mints a shared secret for an inbound webhook.
func generateWebhookSecret() (string, error) {
	return schedcrypto.GenerateCSRFToken()
}

func formInt(r *http.Request, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(r.FormValue(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errValidation("Enter a whole number for " + strings.ReplaceAll(name, "_", " ") + ".")
	}
	return value, nil
}

func formValueOr(r *http.Request, name, fallback string) string {
	if value := strings.TrimSpace(r.FormValue(name)); value != "" {
		return value
	}
	return fallback
}
