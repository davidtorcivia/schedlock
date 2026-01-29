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
	Enabled             bool
	MaxAttempts         int
	BackoffSeconds      []int
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
	Enabled                 bool
	CompletedRequestsDays   int
	AuditLogDays            int
	WebhookFailuresDays     int
	VacuumSchedule          string
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	cfg := &Config{}

	// Server configuration
	cfg.Server = ServerConfig{
		Host:         getEnvAnyDefault(DefaultHost, "SCHEDLOCK_SERVER_HOST", "HOST"),
		Port:         getEnvIntAny(DefaultPort, "SCHEDLOCK_SERVER_PORT", "PORT"),
		BaseURL:      getEnvAnyDefault(DefaultBaseURL, "SCHEDLOCK_BASE_URL", "BASE_URL"),
		ReadTimeout:  getEnvDurationAny(DefaultReadTimeout, "SCHEDLOCK_READ_TIMEOUT", "READ_TIMEOUT"),
		WriteTimeout: getEnvDurationAny(DefaultWriteTimeout, "SCHEDLOCK_WRITE_TIMEOUT", "WRITE_TIMEOUT"),
	}

	// Database configuration
	dataDir := getEnvAnyDefault(DefaultDataDir, "SCHEDLOCK_DATA_DIR", "DATA_DIR")
	dbName := getEnvAnyDefault("schedlock.db", "SCHEDLOCK_DB_NAME", "DB_NAME")
	cfg.Database = DatabaseConfig{
		Path:          filepath.Join(dataDir, dbName),
		WALMode:       true,
		BusyTimeoutMs: DefaultBusyTimeoutMs,
	}

	// Google OAuth configuration
	cfg.Google = GoogleConfig{
		ClientID:     getEnvAny("SCHEDLOCK_GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID"),
		ClientSecret: getEnvAny("SCHEDLOCK_GOOGLE_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET"),
		RedirectURI:  cfg.Server.BaseURL + "/oauth/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/calendar.events"},
	}

	// Approval configuration
	cfg.Approval = ApprovalConfig{
		TimeoutMinutes: getEnvIntAny(DefaultApprovalTimeoutMinutes, "SCHEDLOCK_APPROVAL_TIMEOUT", "APPROVAL_TIMEOUT_MINUTES"),
		DefaultAction:  getEnvAnyDefault(DefaultApprovalDefaultAction, "SCHEDLOCK_APPROVAL_DEFAULT_ACTION", "APPROVAL_DEFAULT_ACTION"),
	}

	// Rate limits configuration
	cfg.RateLimits = RateLimitsConfig{
		Read:  TierLimit{RequestsPerMinute: getEnvIntAny(60, "SCHEDLOCK_RATE_LIMIT_READ", "RATE_LIMIT_READ"), Burst: 10},
		Write: TierLimit{RequestsPerMinute: getEnvIntAny(30, "SCHEDLOCK_RATE_LIMIT_WRITE", "RATE_LIMIT_WRITE"), Burst: 5},
		Admin: TierLimit{RequestsPerMinute: getEnvIntAny(120, "SCHEDLOCK_RATE_LIMIT_ADMIN", "RATE_LIMIT_ADMIN"), Burst: 20},
	}

	// Retry configuration
	cfg.Retry = RetryConfig{
		Enabled:             true,
		MaxAttempts:         3,
		BackoffSeconds:      []int{5, 10, 20},
		RetryableStatusCodes: []int{429, 500, 502, 503},
	}

	// Notification configuration
	cfg.Notifications = NotificationsConfig{
		Ntfy: NtfyConfig{
			Enabled:        getEnvBoolAny(false, "SCHEDLOCK_NTFY_ENABLED", "NTFY_ENABLED"),
			Server:         getEnvAnyDefault("https://ntfy.sh", "SCHEDLOCK_NTFY_SERVER_URL", "SCHEDLOCK_NTFY_SERVER", "NTFY_SERVER"),
			Topic:          getEnvAny("SCHEDLOCK_NTFY_TOPIC", "NTFY_TOPIC"),
			Token:          getEnvAny("SCHEDLOCK_NTFY_TOKEN", "NTFY_TOKEN"),
			Priority:       getEnvAnyDefault("high", "SCHEDLOCK_NTFY_PRIORITY", "NTFY_PRIORITY"),
			MinimalContent: getEnvBoolAny(false, "SCHEDLOCK_NTFY_MINIMAL_CONTENT", "NTFY_MINIMAL_CONTENT"),
		},
		Pushover: PushoverConfig{
			Enabled:  getEnvBoolAny(false, "SCHEDLOCK_PUSHOVER_ENABLED", "PUSHOVER_ENABLED"),
			AppToken: getEnvAny("SCHEDLOCK_PUSHOVER_TOKEN", "SCHEDLOCK_PUSHOVER_APP_TOKEN", "PUSHOVER_APP_TOKEN"),
			UserKey:  getEnvAny("SCHEDLOCK_PUSHOVER_USER_KEY", "PUSHOVER_USER_KEY"),
			Priority: getEnvIntAny(1, "SCHEDLOCK_PUSHOVER_PRIORITY", "PUSHOVER_PRIORITY"),
			Sound:    getEnvAnyDefault("pushover", "SCHEDLOCK_PUSHOVER_SOUND", "PUSHOVER_SOUND"),
		},
		Telegram: TelegramConfig{
			Enabled:             getEnvBoolAny(false, "SCHEDLOCK_TELEGRAM_ENABLED", "TELEGRAM_ENABLED"),
			BotToken:            getEnvAny("SCHEDLOCK_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN"),
			ChatID:              getEnvAny("SCHEDLOCK_TELEGRAM_CHAT_ID", "TELEGRAM_CHAT_ID"),
			WebhookSecret:       getEnvAny("SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_WEBHOOK_SECRET"),
			WebhookPath:         "/webhooks/telegram",
			AutoRegisterWebhook: getEnvBoolAny(true, "SCHEDLOCK_TELEGRAM_AUTO_REGISTER_WEBHOOK", "TELEGRAM_AUTO_REGISTER_WEBHOOK"),
		},
	}

	// Moltbot configuration
	cfg.Moltbot = MoltbotConfig{
		Webhook: WebhookConfig{
			Enabled:          getEnvBoolAny(false, "SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED", "MOLTBOT_WEBHOOK_ENABLED"),
			URL:              getEnvAny("SCHEDLOCK_MOLTBOT_WEBHOOK_URL", "MOLTBOT_WEBHOOK_URL"),
			Token:            getEnvAny("SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET", "SCHEDLOCK_MOLTBOT_WEBHOOK_TOKEN", "MOLTBOT_WEBHOOK_TOKEN"),
			SessionKeyPrefix: "calendar-proxy",
			TimeoutSeconds:   getEnvIntAny(10, "SCHEDLOCK_MOLTBOT_WEBHOOK_TIMEOUT", "MOLTBOT_WEBHOOK_TIMEOUT"),
			MaxRetries:       getEnvIntAny(3, "SCHEDLOCK_MOLTBOT_WEBHOOK_MAX_RETRIES", "MOLTBOT_WEBHOOK_MAX_RETRIES"),
			RetryBackoff:     []int{1, 5, 15},
			NotifyOn:         []string{"approved", "denied", "expired", "change_requested", "completed", "failed"},
		},
	}

	// Auth configuration
	cfg.Auth = AuthConfig{
		AdminPasswordHash: getEnvAny("SCHEDLOCK_AUTH_PASSWORD_HASH", "ADMIN_PASSWORD_HASH"),
		AdminPassword:     getEnvAny("SCHEDLOCK_ADMIN_PASSWORD", "ADMIN_PASSWORD"),
		SecretKey:         getEnvAny("SCHEDLOCK_SERVER_SECRET", "SECRET_KEY", "SCHEDLOCK_SECRET_KEY"),
		EncryptionKey:     getEnvAny("SCHEDLOCK_ENCRYPTION_KEY", "ENCRYPTION_KEY"),
		SessionDuration:   getEnvDurationAny(DefaultSessionDuration, "SCHEDLOCK_SESSION_DURATION", "SESSION_DURATION"),
		SessionRefresh:    getEnvBoolAny(true, "SCHEDLOCK_SESSION_REFRESH", "SESSION_REFRESH"),
		CloudflareAccess: CloudflareAccessConfig{
			Enabled: getEnvBoolAny(false, "SCHEDLOCK_CF_ACCESS_ENABLED", "CF_ACCESS_ENABLED"),
			Team:    getEnvAny("SCHEDLOCK_CF_ACCESS_TEAM", "CF_ACCESS_TEAM"),
			Aud:     getEnvAny("SCHEDLOCK_CF_ACCESS_AUD", "CF_ACCESS_AUD"),
		},
	}

	// Logging configuration
	cfg.Logging = LoggingConfig{
		Level:         getEnvAnyDefault(DefaultLogLevel, "SCHEDLOCK_LOG_LEVEL", "LOG_LEVEL"),
		Format:        getEnvAnyDefault("json", "SCHEDLOCK_LOG_FORMAT", "LOG_FORMAT"),
		IncludeCaller: false,
	}

	// Display configuration
	cfg.Display = DisplayConfig{
		Timezone:       getEnvAnyDefault(DefaultTimezone, "SCHEDLOCK_DISPLAY_TIMEZONE", "DISPLAY_TIMEZONE"),
		DateFormat:     "Jan 2, 2006",
		TimeFormat:     "3:04 PM",
		DatetimeFormat: "Jan 2, 2006 at 3:04 PM",
	}

	// Retention configuration
	cfg.Retention = RetentionConfig{
		Enabled:               true,
		CompletedRequestsDays: getEnvIntAny(DefaultCompletedRequestsDays, "SCHEDLOCK_RETENTION_REQUEST_DAYS", "RETENTION_COMPLETED_DAYS"),
		AuditLogDays:          getEnvIntAny(DefaultAuditLogDays, "SCHEDLOCK_RETENTION_AUDIT_DAYS", "RETENTION_AUDIT_DAYS"),
		WebhookFailuresDays:   getEnvIntAny(DefaultWebhookFailuresDays, "SCHEDLOCK_RETENTION_WEBHOOK_FAILURES_DAYS", "RETENTION_WEBHOOK_FAILURES_DAYS"),
		VacuumSchedule:        "0 3 * * *",
	}

	// Validate required fields
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
