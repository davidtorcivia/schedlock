// Package config handles configuration loading from environment variables and optional YAML files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
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
}

// DatabaseConfig holds SQLite settings.
type DatabaseConfig struct {
	Path          string
	WALMode       bool
	BusyTimeoutMs int
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

// TierLimit defines rate limits for a specific tier.
type TierLimit struct {
	RequestsPerMinute int
	Burst             int
}

// RateLimitsConfig holds rate limiting settings per tier.
type RateLimitsConfig struct {
	Read  TierLimit
	Write TierLimit
	Admin TierLimit
}

// RetryConfig holds retry settings for Google API calls.
type RetryConfig struct {
	Enabled              bool
	MaxAttempts          int
	BackoffSeconds       []int
	RetryableStatusCodes []int
}

// NtfyConfig holds ntfy notification settings.
type NtfyConfig struct {
	Enabled        bool
	Server         string
	Topic          string
	Token          string
	Priority       string
	MinimalContent bool
}

// PushoverConfig holds Pushover notification settings.
type PushoverConfig struct {
	Enabled  bool
	AppToken string
	UserKey  string
	Priority int
	Sound    string
}

// TelegramConfig holds Telegram notification settings.
type TelegramConfig struct {
	Enabled             bool
	BotToken            string
	ChatID              string
	WebhookSecret       string
	WebhookPath         string
	AutoRegisterWebhook bool
}

// NotificationsConfig holds all notification provider settings.
type NotificationsConfig struct {
	Ntfy     NtfyConfig
	Pushover PushoverConfig
	Telegram TelegramConfig
}

// WebhookConfig holds Moltbot webhook settings.
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

// MoltbotConfig holds Moltbot integration settings.
type MoltbotConfig struct {
	Webhook WebhookConfig
}

// CloudflareAccessConfig holds Cloudflare Access settings.
type CloudflareAccessConfig struct {
	Enabled bool
	Team    string
	Aud     string
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	AdminPasswordHash string
	AdminPassword     string // Optional fallback (dev only)
	SecretKey         string
	EncryptionKey     string
	SessionDuration   time.Duration
	SessionRefresh    bool
	CloudflareAccess  CloudflareAccessConfig
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level         string
	Format        string
	IncludeCaller bool
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
	VacuumSchedule        string
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	cfg := defaultConfig()

	dataDir := getEnvAnyDefault(DefaultDataDir, "SCHEDLOCK_DATA_DIR", "DATA_DIR")
	configPath := getEnvAnyDefault(filepath.Join(dataDir, "config.yaml"), "SCHEDLOCK_CONFIG_FILE", "CONFIG_FILE")
	if err := loadConfigFile(cfg, configPath); err != nil {
		return nil, err
	}

	applyEnvOverrides(cfg)

	if cfg.Google.RedirectURI == "" && cfg.Server.BaseURL != "" {
		cfg.Google.RedirectURI = cfg.Server.BaseURL + "/oauth/callback"
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that required configuration fields are set.
func (c *Config) Validate() error {
	if c.Auth.SecretKey == "" {
		return fmt.Errorf("SCHEDLOCK_SERVER_SECRET (or SECRET_KEY) is required")
	}
	if c.Auth.EncryptionKey == "" {
		return fmt.Errorf("SCHEDLOCK_ENCRYPTION_KEY (or ENCRYPTION_KEY) is required")
	}
	if c.Auth.AdminPasswordHash == "" && c.Auth.AdminPassword == "" {
		return fmt.Errorf("SCHEDLOCK_AUTH_PASSWORD_HASH (or ADMIN_PASSWORD_HASH) is required")
	}
	if c.Approval.DefaultAction != "" && c.Approval.DefaultAction != "approve" && c.Approval.DefaultAction != "deny" {
		return fmt.Errorf("approval default action must be approve or deny")
	}
	if c.Logging.Format != "" && c.Logging.Format != "json" && c.Logging.Format != "text" {
		return fmt.Errorf("logging format must be json or text")
	}

	// Validate at least one notification provider is enabled or warn
	if !c.Notifications.Ntfy.Enabled && !c.Notifications.Pushover.Enabled && !c.Notifications.Telegram.Enabled {
		// This is a warning, not an error - web UI still works
		fmt.Println("Warning: No notification providers enabled. Approvals will only be available via Web UI.")
	}

	if c.Auth.AdminPasswordHash == "" && c.Auth.AdminPassword != "" {
		fmt.Println("Warning: ADMIN_PASSWORD provided without hash. Use SCHEDLOCK_AUTH_PASSWORD_HASH for production.")
	}

	return nil
}

// Helper functions for environment variable parsing

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvAny(keys ...string) string {
	for _, key := range keys {
		if value, exists := os.LookupEnv(key); exists {
			return value
		}
	}
	return ""
}

func getEnvAnyDefault(defaultValue string, keys ...string) string {
	if value := getEnvAny(keys...); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvIntAny(defaultValue int, keys ...string) int {
	if value := getEnvAny(keys...); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		lower := strings.ToLower(value)
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return defaultValue
}

func getEnvBoolAny(defaultValue bool, keys ...string) bool {
	if value := getEnvAny(keys...); value != "" {
		lower := strings.ToLower(value)
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value, exists := os.LookupEnv(key); exists {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvDurationAny(defaultValue time.Duration, keys ...string) time.Duration {
	if value := getEnvAny(keys...); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         DefaultHost,
			Port:         DefaultPort,
			BaseURL:      DefaultBaseURL,
			ReadTimeout:  DefaultReadTimeout,
			WriteTimeout: DefaultWriteTimeout,
		},
		Database: DatabaseConfig{
			Path:          filepath.Join(DefaultDataDir, "schedlock.db"),
			WALMode:       true,
			BusyTimeoutMs: DefaultBusyTimeoutMs,
		},
		Google: GoogleConfig{
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
			RetryableStatusCodes: []int{429, 500, 502, 503},
		},
		Notifications: NotificationsConfig{
			Ntfy: NtfyConfig{
				Enabled:        false,
				Server:         "https://ntfy.sh",
				Priority:       "high",
				MinimalContent: false,
			},
			Pushover: PushoverConfig{
				Enabled:  false,
				Priority: 1,
				Sound:    "pushover",
			},
			Telegram: TelegramConfig{
				Enabled:             false,
				WebhookPath:         "/webhooks/telegram",
				AutoRegisterWebhook: true,
			},
		},
		Moltbot: MoltbotConfig{
			Webhook: WebhookConfig{
				Enabled:          false,
				SessionKeyPrefix: "calendar-proxy",
				TimeoutSeconds:   10,
				MaxRetries:       3,
				RetryBackoff:     []int{1, 5, 15},
				NotifyOn:         []string{"approved", "denied", "expired", "change_requested", "completed", "failed"},
			},
		},
		Auth: AuthConfig{
			SessionDuration: DefaultSessionDuration,
			SessionRefresh:  true,
		},
		Logging: LoggingConfig{
			Level:         DefaultLogLevel,
			Format:        "json",
			IncludeCaller: false,
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
			VacuumSchedule:        "0 3 * * *",
		},
	}
}

func applyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}

	cfg.Server.Host = getEnvAnyDefault(cfg.Server.Host, "SCHEDLOCK_SERVER_HOST", "HOST")
	cfg.Server.Port = getEnvIntAny(cfg.Server.Port, "SCHEDLOCK_SERVER_PORT", "PORT")
	cfg.Server.BaseURL = getEnvAnyDefault(cfg.Server.BaseURL, "SCHEDLOCK_BASE_URL", "BASE_URL")
	cfg.Server.ReadTimeout = getEnvDurationAny(cfg.Server.ReadTimeout, "SCHEDLOCK_READ_TIMEOUT", "READ_TIMEOUT")
	cfg.Server.WriteTimeout = getEnvDurationAny(cfg.Server.WriteTimeout, "SCHEDLOCK_WRITE_TIMEOUT", "WRITE_TIMEOUT")

	dataDir := getEnvAny("SCHEDLOCK_DATA_DIR", "DATA_DIR")
	dbName := getEnvAny("SCHEDLOCK_DB_NAME", "DB_NAME")
	if dataDir != "" || dbName != "" {
		if dataDir == "" {
			dataDir = filepath.Dir(cfg.Database.Path)
			if dataDir == "" {
				dataDir = DefaultDataDir
			}
		}
		if dbName == "" {
			dbName = filepath.Base(cfg.Database.Path)
			if dbName == "" {
				dbName = "schedlock.db"
			}
		}
		cfg.Database.Path = filepath.Join(dataDir, dbName)
	}

	cfg.Google.ClientID = getEnvAnyDefault(cfg.Google.ClientID, "SCHEDLOCK_GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID")
	cfg.Google.ClientSecret = getEnvAnyDefault(cfg.Google.ClientSecret, "SCHEDLOCK_GOOGLE_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET")
	cfg.Google.RedirectURI = getEnvAnyDefault(cfg.Google.RedirectURI, "SCHEDLOCK_GOOGLE_REDIRECT_URI", "GOOGLE_REDIRECT_URI")

	cfg.Approval.TimeoutMinutes = getEnvIntAny(cfg.Approval.TimeoutMinutes, "SCHEDLOCK_APPROVAL_TIMEOUT", "APPROVAL_TIMEOUT_MINUTES")
	cfg.Approval.DefaultAction = getEnvAnyDefault(cfg.Approval.DefaultAction, "SCHEDLOCK_APPROVAL_DEFAULT_ACTION", "APPROVAL_DEFAULT_ACTION")

	cfg.RateLimits.Read.RequestsPerMinute = getEnvIntAny(cfg.RateLimits.Read.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_READ", "RATE_LIMIT_READ")
	cfg.RateLimits.Write.RequestsPerMinute = getEnvIntAny(cfg.RateLimits.Write.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_WRITE", "RATE_LIMIT_WRITE")
	cfg.RateLimits.Admin.RequestsPerMinute = getEnvIntAny(cfg.RateLimits.Admin.RequestsPerMinute, "SCHEDLOCK_RATE_LIMIT_ADMIN", "RATE_LIMIT_ADMIN")

	cfg.Notifications.Ntfy.Enabled = getEnvBoolAny(cfg.Notifications.Ntfy.Enabled, "SCHEDLOCK_NTFY_ENABLED", "NTFY_ENABLED")
	cfg.Notifications.Ntfy.Server = getEnvAnyDefault(cfg.Notifications.Ntfy.Server, "SCHEDLOCK_NTFY_SERVER_URL", "SCHEDLOCK_NTFY_SERVER", "NTFY_SERVER")
	cfg.Notifications.Ntfy.Topic = getEnvAnyDefault(cfg.Notifications.Ntfy.Topic, "SCHEDLOCK_NTFY_TOPIC", "NTFY_TOPIC")
	cfg.Notifications.Ntfy.Token = getEnvAnyDefault(cfg.Notifications.Ntfy.Token, "SCHEDLOCK_NTFY_TOKEN", "NTFY_TOKEN")
	cfg.Notifications.Ntfy.Priority = getEnvAnyDefault(cfg.Notifications.Ntfy.Priority, "SCHEDLOCK_NTFY_PRIORITY", "NTFY_PRIORITY")
	cfg.Notifications.Ntfy.MinimalContent = getEnvBoolAny(cfg.Notifications.Ntfy.MinimalContent, "SCHEDLOCK_NTFY_MINIMAL_CONTENT", "NTFY_MINIMAL_CONTENT")

	cfg.Notifications.Pushover.Enabled = getEnvBoolAny(cfg.Notifications.Pushover.Enabled, "SCHEDLOCK_PUSHOVER_ENABLED", "PUSHOVER_ENABLED")
	cfg.Notifications.Pushover.AppToken = getEnvAnyDefault(cfg.Notifications.Pushover.AppToken, "SCHEDLOCK_PUSHOVER_TOKEN", "SCHEDLOCK_PUSHOVER_APP_TOKEN", "PUSHOVER_APP_TOKEN")
	cfg.Notifications.Pushover.UserKey = getEnvAnyDefault(cfg.Notifications.Pushover.UserKey, "SCHEDLOCK_PUSHOVER_USER_KEY", "PUSHOVER_USER_KEY")
	cfg.Notifications.Pushover.Priority = getEnvIntAny(cfg.Notifications.Pushover.Priority, "SCHEDLOCK_PUSHOVER_PRIORITY", "PUSHOVER_PRIORITY")
	cfg.Notifications.Pushover.Sound = getEnvAnyDefault(cfg.Notifications.Pushover.Sound, "SCHEDLOCK_PUSHOVER_SOUND", "PUSHOVER_SOUND")

	cfg.Notifications.Telegram.Enabled = getEnvBoolAny(cfg.Notifications.Telegram.Enabled, "SCHEDLOCK_TELEGRAM_ENABLED", "TELEGRAM_ENABLED")
	cfg.Notifications.Telegram.BotToken = getEnvAnyDefault(cfg.Notifications.Telegram.BotToken, "SCHEDLOCK_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN")
	cfg.Notifications.Telegram.ChatID = getEnvAnyDefault(cfg.Notifications.Telegram.ChatID, "SCHEDLOCK_TELEGRAM_CHAT_ID", "TELEGRAM_CHAT_ID")
	cfg.Notifications.Telegram.WebhookSecret = getEnvAnyDefault(cfg.Notifications.Telegram.WebhookSecret, "SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_WEBHOOK_SECRET")
	cfg.Notifications.Telegram.AutoRegisterWebhook = getEnvBoolAny(cfg.Notifications.Telegram.AutoRegisterWebhook, "SCHEDLOCK_TELEGRAM_AUTO_REGISTER_WEBHOOK", "TELEGRAM_AUTO_REGISTER_WEBHOOK")

	cfg.Moltbot.Webhook.Enabled = getEnvBoolAny(cfg.Moltbot.Webhook.Enabled, "SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED", "MOLTBOT_WEBHOOK_ENABLED")
	cfg.Moltbot.Webhook.URL = getEnvAnyDefault(cfg.Moltbot.Webhook.URL, "SCHEDLOCK_MOLTBOT_WEBHOOK_URL", "MOLTBOT_WEBHOOK_URL")
	cfg.Moltbot.Webhook.Token = getEnvAnyDefault(cfg.Moltbot.Webhook.Token, "SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET", "SCHEDLOCK_MOLTBOT_WEBHOOK_TOKEN", "MOLTBOT_WEBHOOK_TOKEN")
	cfg.Moltbot.Webhook.TimeoutSeconds = getEnvIntAny(cfg.Moltbot.Webhook.TimeoutSeconds, "SCHEDLOCK_MOLTBOT_WEBHOOK_TIMEOUT", "MOLTBOT_WEBHOOK_TIMEOUT")
	cfg.Moltbot.Webhook.MaxRetries = getEnvIntAny(cfg.Moltbot.Webhook.MaxRetries, "SCHEDLOCK_MOLTBOT_WEBHOOK_MAX_RETRIES", "MOLTBOT_WEBHOOK_MAX_RETRIES")

	cfg.Auth.AdminPasswordHash = getEnvAnyDefault(cfg.Auth.AdminPasswordHash, "SCHEDLOCK_AUTH_PASSWORD_HASH", "ADMIN_PASSWORD_HASH")
	cfg.Auth.AdminPassword = getEnvAnyDefault(cfg.Auth.AdminPassword, "SCHEDLOCK_ADMIN_PASSWORD", "ADMIN_PASSWORD")
	cfg.Auth.SecretKey = getEnvAnyDefault(cfg.Auth.SecretKey, "SCHEDLOCK_SERVER_SECRET", "SECRET_KEY", "SCHEDLOCK_SECRET_KEY")
	cfg.Auth.EncryptionKey = getEnvAnyDefault(cfg.Auth.EncryptionKey, "SCHEDLOCK_ENCRYPTION_KEY", "ENCRYPTION_KEY")
	cfg.Auth.SessionDuration = getEnvDurationAny(cfg.Auth.SessionDuration, "SCHEDLOCK_SESSION_DURATION", "SESSION_DURATION")
	cfg.Auth.SessionRefresh = getEnvBoolAny(cfg.Auth.SessionRefresh, "SCHEDLOCK_SESSION_REFRESH", "SESSION_REFRESH")
	cfg.Auth.CloudflareAccess.Enabled = getEnvBoolAny(cfg.Auth.CloudflareAccess.Enabled, "SCHEDLOCK_CF_ACCESS_ENABLED", "CF_ACCESS_ENABLED")
	cfg.Auth.CloudflareAccess.Team = getEnvAnyDefault(cfg.Auth.CloudflareAccess.Team, "SCHEDLOCK_CF_ACCESS_TEAM", "CF_ACCESS_TEAM")
	cfg.Auth.CloudflareAccess.Aud = getEnvAnyDefault(cfg.Auth.CloudflareAccess.Aud, "SCHEDLOCK_CF_ACCESS_AUD", "CF_ACCESS_AUD")

	cfg.Logging.Level = getEnvAnyDefault(cfg.Logging.Level, "SCHEDLOCK_LOG_LEVEL", "LOG_LEVEL")
	cfg.Logging.Format = getEnvAnyDefault(cfg.Logging.Format, "SCHEDLOCK_LOG_FORMAT", "LOG_FORMAT")

	cfg.Display.Timezone = getEnvAnyDefault(cfg.Display.Timezone, "SCHEDLOCK_DISPLAY_TIMEZONE", "DISPLAY_TIMEZONE")

	cfg.Retention.CompletedRequestsDays = getEnvIntAny(cfg.Retention.CompletedRequestsDays, "SCHEDLOCK_RETENTION_REQUEST_DAYS", "RETENTION_COMPLETED_DAYS")
	cfg.Retention.AuditLogDays = getEnvIntAny(cfg.Retention.AuditLogDays, "SCHEDLOCK_RETENTION_AUDIT_DAYS", "RETENTION_AUDIT_DAYS")
	cfg.Retention.WebhookFailuresDays = getEnvIntAny(cfg.Retention.WebhookFailuresDays, "SCHEDLOCK_RETENTION_WEBHOOK_FAILURES_DAYS", "RETENTION_WEBHOOK_FAILURES_DAYS")
}
