// Package config loads configuration from a YAML file and the environment.
//
// Precedence, lowest to highest: built-in defaults, the config file, the
// environment, then the runtime settings an operator edits in the web UI.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the complete application configuration.
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Google        GoogleConfig
	Approval      ApprovalConfig
	RateLimits    RateLimitsConfig
	Retry         RetryConfig
	Notifications NotificationsConfig
	Moltbot       MoltbotConfig
	Auth          AuthConfig
	Logging       LoggingConfig
	Display       DisplayConfig
	Retention     RetentionConfig
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host         string
	Port         int
	BaseURL      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// TrustedProxies lists the addresses or CIDR blocks whose forwarded-for
	// headers may be believed. Empty means the peer address is always used.
	TrustedProxies []string
}

// DatabaseConfig holds SQLite settings.
type DatabaseConfig struct {
	Path string
}

// GoogleConfig holds Google OAuth settings.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
}

// ApprovalConfig holds approval workflow settings.
type ApprovalConfig struct {
	TimeoutMinutes int
	DefaultAction  string // "approve" or "deny"
}

// TierLimit is the rate limit for one API key tier.
type TierLimit struct {
	RequestsPerMinute int
	Burst             int
}

// RateLimitsConfig holds the per-tier rate limits.
type RateLimitsConfig struct {
	Read  TierLimit
	Write TierLimit
	Admin TierLimit
}

// RetryConfig controls retries of failed calendar operations.
type RetryConfig struct {
	Enabled              bool
	MaxAttempts          int
	BackoffSeconds       []int
	RetryableStatusCodes []int
}

// NtfyConfig holds ntfy settings.
type NtfyConfig struct {
	Enabled        bool
	Server         string
	Topic          string
	Token          string
	Priority       string
	MinimalContent bool
}

// PushoverConfig holds Pushover settings.
type PushoverConfig struct {
	Enabled  bool
	AppToken string
	UserKey  string
	Priority int
	Sound    string
}

// TelegramConfig holds Telegram settings.
type TelegramConfig struct {
	Enabled             bool
	BotToken            string
	ChatID              string
	WebhookSecret       string
	WebhookPath         string
	AutoRegisterWebhook bool
}

// GenericWebhookConfig holds generic webhook notification settings.
type GenericWebhookConfig struct {
	Enabled        bool
	URL            string
	Secret         string
	TimeoutSeconds int
}

// NotificationsConfig holds every notification provider's settings.
type NotificationsConfig struct {
	Ntfy     NtfyConfig
	Pushover PushoverConfig
	Telegram TelegramConfig
	Webhook  GenericWebhookConfig
}

// WebhookConfig holds the status webhook delivered to the calling system.
type WebhookConfig struct {
	Enabled          bool
	URL              string
	Token            string
	SessionKeyPrefix string
	TimeoutSeconds   int
	MaxRetries       int
	RetryBackoff     []int
	NotifyOn         []string
}

// MoltbotConfig holds the calling system's integration settings.
type MoltbotConfig struct {
	Webhook WebhookConfig
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	AdminPasswordHash string
	AdminPassword     string // Development fallback; not for production use.
	SecretKey         string
	EncryptionKey     string
	SessionDuration   time.Duration
	SessionRefresh    bool
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string
	Format string
}

// DisplayConfig holds display formatting settings.
type DisplayConfig struct {
	Timezone       string
	DateFormat     string
	TimeFormat     string
	DatetimeFormat string
}

// RetentionConfig holds data retention settings.
type RetentionConfig struct {
	Enabled               bool
	CompletedRequestsDays int
	AuditLogDays          int
	WebhookFailuresDays   int
}

// Load reads the configuration and reports whether first-run setup is needed.
//
// Setup mode is entered when no admin password is configured: the instance
// cannot authenticate anyone, so it serves the wizard instead of the app.
func Load() (cfg *Config, needsSetup bool, err error) {
	cfg = defaultConfig()

	if err := loadConfigFile(cfg, GetConfigFilePath()); err != nil {
		return nil, false, err
	}
	applyEnvOverrides(cfg)

	if cfg.Google.RedirectURI == "" && cfg.Server.BaseURL != "" {
		cfg.Google.RedirectURI = strings.TrimRight(cfg.Server.BaseURL, "/") + "/oauth/callback"
	}

	if cfg.Auth.AdminPasswordHash == "" && cfg.Auth.AdminPassword == "" {
		// Generate the secrets the wizard will persist, so an operator never has
		// to produce them by hand.
		if cfg.Auth.SecretKey == "" {
			if cfg.Auth.SecretKey, err = generateSecret(); err != nil {
				return nil, false, fmt.Errorf("failed to generate a server secret: %w", err)
			}
		}
		if cfg.Auth.EncryptionKey == "" {
			if cfg.Auth.EncryptionKey, err = generateSecret(); err != nil {
				return nil, false, fmt.Errorf("failed to generate an encryption key: %w", err)
			}
		}
		return cfg, true, nil
	}

	if err := cfg.Validate(); err != nil {
		return nil, false, err
	}

	return cfg, false, nil
}

// Validate reports whether the configuration can run the application.
func (c *Config) Validate() error {
	if c.Auth.SecretKey == "" {
		return fmt.Errorf("a server secret is required (SCHEDLOCK_SERVER_SECRET)")
	}
	if c.Auth.EncryptionKey == "" {
		return fmt.Errorf("an encryption key is required (SCHEDLOCK_ENCRYPTION_KEY)")
	}
	if c.Auth.AdminPasswordHash == "" && c.Auth.AdminPassword == "" {
		return fmt.Errorf("an admin password hash is required (SCHEDLOCK_AUTH_PASSWORD_HASH)")
	}
	switch c.Approval.DefaultAction {
	case "", "approve", "deny":
	default:
		return fmt.Errorf("the approval default action must be approve or deny")
	}
	switch c.Logging.Format {
	case "", "json", "text":
	default:
		return fmt.Errorf("the log format must be json or text")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("the server port must be between 1 and 65535")
	}
	return nil
}

// Warnings returns advisory messages about a valid but questionable
// configuration.
func (c *Config) Warnings() []string {
	var warnings []string

	if !c.Notifications.Ntfy.Enabled && !c.Notifications.Pushover.Enabled &&
		!c.Notifications.Telegram.Enabled && !c.Notifications.Webhook.Enabled {
		warnings = append(warnings,
			"No notification provider is enabled in the file or environment configuration. "+
				"Approvals will only be reachable through the web UI unless a provider is configured in Settings.")
	}

	if c.Auth.AdminPasswordHash == "" && c.Auth.AdminPassword != "" {
		warnings = append(warnings,
			"A plaintext admin password is configured. Use SCHEDLOCK_AUTH_PASSWORD_HASH in production.")
	}

	if c.Approval.DefaultAction == "approve" {
		warnings = append(warnings,
			"Requests that time out will be approved automatically. "+
				"An unattended request will reach the calendar without review.")
	}

	if strings.HasPrefix(c.Server.BaseURL, "http://") && !isLoopback(c.Server.BaseURL) {
		warnings = append(warnings,
			"The base URL is not HTTPS. Approval links and session cookies will travel unencrypted.")
	}

	return warnings
}

func isLoopback(baseURL string) bool {
	return strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1")
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         DefaultHost,
			Port:         DefaultPort,
			BaseURL:      DefaultBaseURL,
			ReadTimeout:  DefaultReadTimeout,
			WriteTimeout: DefaultWriteTimeout,
			IdleTimeout:  DefaultIdleTimeout,
		},
		Database: DatabaseConfig{
			Path: filepath.Join(DefaultDataDir, "schedlock.db"),
		},
		Google: GoogleConfig{
			// events is the narrowest scope that still permits the operations
			// this proxy exposes; it grants no access to calendar settings or
			// sharing.
			Scopes: []string{"https://www.googleapis.com/auth/calendar.events"},
		},
		Approval: ApprovalConfig{
			TimeoutMinutes: DefaultApprovalTimeoutMinutes,
			DefaultAction:  DefaultApprovalDefaultAction,
		},
		RateLimits: RateLimitsConfig{
			Read:  TierLimit{RequestsPerMinute: 60, Burst: 10},
			Write: TierLimit{RequestsPerMinute: 30, Burst: 5},
			Admin: TierLimit{RequestsPerMinute: 120, Burst: 20},
		},
		Retry: RetryConfig{
			Enabled:              true,
			MaxAttempts:          3,
			BackoffSeconds:       []int{5, 10, 20},
			RetryableStatusCodes: []int{429, 500, 502, 503, 504},
		},
		Notifications: NotificationsConfig{
			Ntfy:     NtfyConfig{Server: "https://ntfy.sh", Priority: "high"},
			Pushover: PushoverConfig{Priority: 1, Sound: "pushover"},
			Telegram: TelegramConfig{WebhookPath: "/webhooks/telegram", AutoRegisterWebhook: true},
			Webhook:  GenericWebhookConfig{TimeoutSeconds: 10},
		},
		Moltbot: MoltbotConfig{
			Webhook: WebhookConfig{
				SessionKeyPrefix: "calendar-proxy",
				TimeoutSeconds:   10,
				MaxRetries:       3,
				RetryBackoff:     []int{1, 5, 15},
				NotifyOn: []string{
					"approved", "denied", "expired", "change_requested", "completed", "failed",
				},
			},
		},
		Auth: AuthConfig{
			SessionDuration: DefaultSessionDuration,
			SessionRefresh:  true,
		},
		Logging: LoggingConfig{
			Level:  DefaultLogLevel,
			Format: "json",
		},
		Display: DisplayConfig{
			Timezone:       DefaultTimezone,
			DateFormat:     "Jan 2, 2006",
			TimeFormat:     "3:04 PM",
			DatetimeFormat: "Jan 2, 2006 at 3:04 PM",
		},
		Retention: RetentionConfig{
			Enabled:               true,
			CompletedRequestsDays: DefaultCompletedRequestsDays,
			AuditLogDays:          DefaultAuditLogDays,
			WebhookFailuresDays:   DefaultWebhookFailuresDays,
		},
	}
}

// applyEnvOverrides layers environment variables over the loaded config.
//
// Each setting accepts a SCHEDLOCK_-prefixed name and, for compatibility with
// older deployments, an unprefixed one.
func applyEnvOverrides(cfg *Config) {
	cfg.Server.Host = envString(cfg.Server.Host, "SCHEDLOCK_SERVER_HOST", "HOST")
	cfg.Server.Port = envInt(cfg.Server.Port, "SCHEDLOCK_SERVER_PORT", "PORT")
	cfg.Server.BaseURL = strings.TrimRight(envString(cfg.Server.BaseURL, "SCHEDLOCK_BASE_URL", "BASE_URL"), "/")
	cfg.Server.ReadTimeout = envDuration(cfg.Server.ReadTimeout, "SCHEDLOCK_READ_TIMEOUT", "READ_TIMEOUT")
	cfg.Server.WriteTimeout = envDuration(cfg.Server.WriteTimeout, "SCHEDLOCK_WRITE_TIMEOUT", "WRITE_TIMEOUT")
	cfg.Server.IdleTimeout = envDuration(cfg.Server.IdleTimeout, "SCHEDLOCK_IDLE_TIMEOUT")
	cfg.Server.TrustedProxies = envList(cfg.Server.TrustedProxies, "SCHEDLOCK_TRUSTED_PROXIES")

	if dataDir := envAny("SCHEDLOCK_DATA_DIR", "DATA_DIR"); dataDir != "" {
		cfg.Database.Path = filepath.Join(dataDir, filepath.Base(cfg.Database.Path))
	}
	if dbName := envAny("SCHEDLOCK_DB_NAME", "DB_NAME"); dbName != "" {
		cfg.Database.Path = filepath.Join(filepath.Dir(cfg.Database.Path), dbName)
	}

	cfg.Google.ClientID = envString(cfg.Google.ClientID, "SCHEDLOCK_GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID")
	cfg.Google.ClientSecret = envString(cfg.Google.ClientSecret, "SCHEDLOCK_GOOGLE_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET")
	cfg.Google.RedirectURI = envString(cfg.Google.RedirectURI, "SCHEDLOCK_GOOGLE_REDIRECT_URI", "GOOGLE_REDIRECT_URI")

	cfg.Approval.TimeoutMinutes = envInt(cfg.Approval.TimeoutMinutes, "SCHEDLOCK_APPROVAL_TIMEOUT", "APPROVAL_TIMEOUT_MINUTES")
	cfg.Approval.DefaultAction = envString(cfg.Approval.DefaultAction, "SCHEDLOCK_APPROVAL_DEFAULT_ACTION", "APPROVAL_DEFAULT_ACTION")

	cfg.RateLimits.Read.RequestsPerMinute = envInt(cfg.RateLimits.Read.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_READ", "RATE_LIMIT_READ")
	cfg.RateLimits.Write.RequestsPerMinute = envInt(cfg.RateLimits.Write.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_WRITE", "RATE_LIMIT_WRITE")
	cfg.RateLimits.Admin.RequestsPerMinute = envInt(cfg.RateLimits.Admin.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_ADMIN", "RATE_LIMIT_ADMIN")

	cfg.Notifications.Ntfy.Enabled = envBool(cfg.Notifications.Ntfy.Enabled, "SCHEDLOCK_NTFY_ENABLED", "NTFY_ENABLED")
	cfg.Notifications.Ntfy.Server = envString(cfg.Notifications.Ntfy.Server, "SCHEDLOCK_NTFY_SERVER_URL", "SCHEDLOCK_NTFY_SERVER", "NTFY_SERVER")
	cfg.Notifications.Ntfy.Topic = envString(cfg.Notifications.Ntfy.Topic, "SCHEDLOCK_NTFY_TOPIC", "NTFY_TOPIC")
	cfg.Notifications.Ntfy.Token = envString(cfg.Notifications.Ntfy.Token, "SCHEDLOCK_NTFY_TOKEN", "NTFY_TOKEN")
	cfg.Notifications.Ntfy.Priority = envString(cfg.Notifications.Ntfy.Priority, "SCHEDLOCK_NTFY_PRIORITY", "NTFY_PRIORITY")
	cfg.Notifications.Ntfy.MinimalContent = envBool(cfg.Notifications.Ntfy.MinimalContent, "SCHEDLOCK_NTFY_MINIMAL_CONTENT", "NTFY_MINIMAL_CONTENT")

	cfg.Notifications.Pushover.Enabled = envBool(cfg.Notifications.Pushover.Enabled, "SCHEDLOCK_PUSHOVER_ENABLED", "PUSHOVER_ENABLED")
	cfg.Notifications.Pushover.AppToken = envString(cfg.Notifications.Pushover.AppToken, "SCHEDLOCK_PUSHOVER_TOKEN", "SCHEDLOCK_PUSHOVER_APP_TOKEN", "PUSHOVER_APP_TOKEN")
	cfg.Notifications.Pushover.UserKey = envString(cfg.Notifications.Pushover.UserKey, "SCHEDLOCK_PUSHOVER_USER_KEY", "PUSHOVER_USER_KEY")
	cfg.Notifications.Pushover.Priority = envInt(cfg.Notifications.Pushover.Priority, "SCHEDLOCK_PUSHOVER_PRIORITY", "PUSHOVER_PRIORITY")
	cfg.Notifications.Pushover.Sound = envString(cfg.Notifications.Pushover.Sound, "SCHEDLOCK_PUSHOVER_SOUND", "PUSHOVER_SOUND")

	cfg.Notifications.Telegram.Enabled = envBool(cfg.Notifications.Telegram.Enabled, "SCHEDLOCK_TELEGRAM_ENABLED", "TELEGRAM_ENABLED")
	cfg.Notifications.Telegram.BotToken = envString(cfg.Notifications.Telegram.BotToken, "SCHEDLOCK_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN")
	cfg.Notifications.Telegram.ChatID = envString(cfg.Notifications.Telegram.ChatID, "SCHEDLOCK_TELEGRAM_CHAT_ID", "TELEGRAM_CHAT_ID")
	cfg.Notifications.Telegram.WebhookSecret = envString(cfg.Notifications.Telegram.WebhookSecret, "SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_WEBHOOK_SECRET")
	cfg.Notifications.Telegram.WebhookPath = envString(cfg.Notifications.Telegram.WebhookPath, "SCHEDLOCK_TELEGRAM_WEBHOOK_PATH")
	cfg.Notifications.Telegram.AutoRegisterWebhook = envBool(cfg.Notifications.Telegram.AutoRegisterWebhook, "SCHEDLOCK_TELEGRAM_AUTO_REGISTER_WEBHOOK", "TELEGRAM_AUTO_REGISTER_WEBHOOK")

	cfg.Notifications.Webhook.Enabled = envBool(cfg.Notifications.Webhook.Enabled, "SCHEDLOCK_WEBHOOK_ENABLED", "WEBHOOK_ENABLED")
	cfg.Notifications.Webhook.URL = envString(cfg.Notifications.Webhook.URL, "SCHEDLOCK_WEBHOOK_URL", "WEBHOOK_URL")
	cfg.Notifications.Webhook.Secret = envString(cfg.Notifications.Webhook.Secret, "SCHEDLOCK_WEBHOOK_SECRET", "WEBHOOK_SECRET")
	cfg.Notifications.Webhook.TimeoutSeconds = envInt(cfg.Notifications.Webhook.TimeoutSeconds, "SCHEDLOCK_WEBHOOK_TIMEOUT", "WEBHOOK_TIMEOUT")

	cfg.Moltbot.Webhook.Enabled = envBool(cfg.Moltbot.Webhook.Enabled, "SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED", "MOLTBOT_WEBHOOK_ENABLED")
	cfg.Moltbot.Webhook.URL = envString(cfg.Moltbot.Webhook.URL, "SCHEDLOCK_MOLTBOT_WEBHOOK_URL", "MOLTBOT_WEBHOOK_URL")
	cfg.Moltbot.Webhook.Token = envString(cfg.Moltbot.Webhook.Token, "SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET", "SCHEDLOCK_MOLTBOT_WEBHOOK_TOKEN", "MOLTBOT_WEBHOOK_TOKEN")
	cfg.Moltbot.Webhook.TimeoutSeconds = envInt(cfg.Moltbot.Webhook.TimeoutSeconds, "SCHEDLOCK_MOLTBOT_WEBHOOK_TIMEOUT", "MOLTBOT_WEBHOOK_TIMEOUT")
	cfg.Moltbot.Webhook.MaxRetries = envInt(cfg.Moltbot.Webhook.MaxRetries, "SCHEDLOCK_MOLTBOT_WEBHOOK_MAX_RETRIES", "MOLTBOT_WEBHOOK_MAX_RETRIES")

	cfg.Auth.AdminPasswordHash = envString(cfg.Auth.AdminPasswordHash, "SCHEDLOCK_AUTH_PASSWORD_HASH", "ADMIN_PASSWORD_HASH")
	cfg.Auth.AdminPassword = envString(cfg.Auth.AdminPassword, "SCHEDLOCK_ADMIN_PASSWORD", "ADMIN_PASSWORD")
	cfg.Auth.SecretKey = envString(cfg.Auth.SecretKey, "SCHEDLOCK_SERVER_SECRET", "SECRET_KEY", "SCHEDLOCK_SECRET_KEY")
	cfg.Auth.EncryptionKey = envString(cfg.Auth.EncryptionKey, "SCHEDLOCK_ENCRYPTION_KEY", "ENCRYPTION_KEY")
	cfg.Auth.SessionDuration = envDuration(cfg.Auth.SessionDuration, "SCHEDLOCK_SESSION_DURATION", "SESSION_DURATION")
	cfg.Auth.SessionRefresh = envBool(cfg.Auth.SessionRefresh, "SCHEDLOCK_SESSION_REFRESH", "SESSION_REFRESH")

	cfg.Logging.Level = envString(cfg.Logging.Level, "SCHEDLOCK_LOG_LEVEL", "LOG_LEVEL")
	cfg.Logging.Format = envString(cfg.Logging.Format, "SCHEDLOCK_LOG_FORMAT", "LOG_FORMAT")

	cfg.Display.Timezone = envString(cfg.Display.Timezone, "SCHEDLOCK_DISPLAY_TIMEZONE", "DISPLAY_TIMEZONE")

	cfg.Retention.Enabled = envBool(cfg.Retention.Enabled, "SCHEDLOCK_RETENTION_ENABLED")
	cfg.Retention.CompletedRequestsDays = envInt(cfg.Retention.CompletedRequestsDays, "SCHEDLOCK_RETENTION_REQUEST_DAYS", "RETENTION_COMPLETED_DAYS")
	cfg.Retention.AuditLogDays = envInt(cfg.Retention.AuditLogDays, "SCHEDLOCK_RETENTION_AUDIT_DAYS", "RETENTION_AUDIT_DAYS")
	cfg.Retention.WebhookFailuresDays = envInt(cfg.Retention.WebhookFailuresDays, "SCHEDLOCK_RETENTION_WEBHOOK_FAILURES_DAYS", "RETENTION_WEBHOOK_FAILURES_DAYS")
}

// envAny returns the first environment variable that is set among keys.
func envAny(keys ...string) string {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
	}
	return ""
}

func envString(fallback string, keys ...string) string {
	if value := envAny(keys...); value != "" {
		return value
	}
	return fallback
}

func envInt(fallback int, keys ...string) int {
	value := envAny(keys...)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: ignoring non-numeric value %q for %s\n", value, keys[0])
		return fallback
	}
	return parsed
}

func envBool(fallback bool, keys ...string) bool {
	value := envAny(keys...)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		fmt.Fprintf(os.Stderr, "Warning: ignoring non-boolean value %q for %s\n", value, keys[0])
		return fallback
	}
}

func envDuration(fallback time.Duration, keys ...string) time.Duration {
	value := envAny(keys...)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: ignoring invalid duration %q for %s\n", value, keys[0])
		return fallback
	}
	return parsed
}

func envList(fallback []string, keys ...string) []string {
	value := envAny(keys...)
	if value == "" {
		return fallback
	}

	var items []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// generateSecret returns a fresh 256-bit secret, base64-encoded.
func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
