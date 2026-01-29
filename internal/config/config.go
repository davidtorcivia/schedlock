// Package config handles configuration loading from environment variables and optional YAML files.
package config

import (
	"fmt"
	"os"
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
	AdminPassword    string
	SecretKey        string
	EncryptionKey    string
	SessionDuration  time.Duration
	SessionRefresh   bool
	CloudflareAccess CloudflareAccessConfig
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
		Host:         getEnv("HOST", DefaultHost),
		Port:         getEnvInt("PORT", DefaultPort),
		BaseURL:      getEnv("BASE_URL", DefaultBaseURL),
		ReadTimeout:  getEnvDuration("READ_TIMEOUT", DefaultReadTimeout),
		WriteTimeout: getEnvDuration("WRITE_TIMEOUT", DefaultWriteTimeout),
	}

	// Database configuration
	cfg.Database = DatabaseConfig{
		Path:          getEnv("DATA_DIR", DefaultDataDir) + "/schedlock.db",
		WALMode:       true,
		BusyTimeoutMs: DefaultBusyTimeoutMs,
	}

	// Google OAuth configuration
	cfg.Google = GoogleConfig{
		ClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		ClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		RedirectURI:  cfg.Server.BaseURL + "/oauth/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/calendar.events"},
	}

	// Approval configuration
	cfg.Approval = ApprovalConfig{
		TimeoutMinutes: getEnvInt("APPROVAL_TIMEOUT_MINUTES", DefaultApprovalTimeoutMinutes),
		DefaultAction:  getEnv("APPROVAL_DEFAULT_ACTION", DefaultApprovalDefaultAction),
	}

	// Rate limits configuration
	cfg.RateLimits = RateLimitsConfig{
		Read:  TierLimit{RequestsPerMinute: 60, Burst: 10},
		Write: TierLimit{RequestsPerMinute: 30, Burst: 5},
		Admin: TierLimit{RequestsPerMinute: 120, Burst: 20},
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
			Enabled:        getEnvBool("NTFY_ENABLED", false),
			Server:         getEnv("NTFY_SERVER", "https://ntfy.sh"),
			Topic:          getEnv("NTFY_TOPIC", ""),
			Token:          getEnv("NTFY_TOKEN", ""),
			Priority:       getEnv("NTFY_PRIORITY", "high"),
			MinimalContent: getEnvBool("NTFY_MINIMAL_CONTENT", false),
		},
		Pushover: PushoverConfig{
			Enabled:  getEnvBool("PUSHOVER_ENABLED", false),
			AppToken: getEnv("PUSHOVER_APP_TOKEN", ""),
			UserKey:  getEnv("PUSHOVER_USER_KEY", ""),
			Priority: getEnvInt("PUSHOVER_PRIORITY", 1),
			Sound:    getEnv("PUSHOVER_SOUND", "pushover"),
		},
		Telegram: TelegramConfig{
			Enabled:             getEnvBool("TELEGRAM_ENABLED", false),
			BotToken:            getEnv("TELEGRAM_BOT_TOKEN", ""),
			ChatID:              getEnv("TELEGRAM_CHAT_ID", ""),
			WebhookSecret:       getEnv("TELEGRAM_WEBHOOK_SECRET", ""),
			WebhookPath:         "/webhooks/telegram",
			AutoRegisterWebhook: getEnvBool("TELEGRAM_AUTO_REGISTER_WEBHOOK", true),
		},
	}

	// Moltbot configuration
	cfg.Moltbot = MoltbotConfig{
		Webhook: WebhookConfig{
			Enabled:          getEnvBool("MOLTBOT_WEBHOOK_ENABLED", false),
			URL:              getEnv("MOLTBOT_WEBHOOK_URL", ""),
			Token:            getEnv("MOLTBOT_WEBHOOK_TOKEN", ""),
			SessionKeyPrefix: "calendar-proxy",
			TimeoutSeconds:   10,
			MaxRetries:       3,
			RetryBackoff:     []int{1, 5, 15},
			NotifyOn:         []string{"approved", "denied", "expired", "change_requested", "completed", "failed"},
		},
	}

	// Auth configuration
	cfg.Auth = AuthConfig{
		AdminPassword:   getEnv("ADMIN_PASSWORD", ""),
		SecretKey:       getEnv("SECRET_KEY", ""),
		EncryptionKey:   getEnv("ENCRYPTION_KEY", ""),
		SessionDuration: getEnvDuration("SESSION_DURATION", DefaultSessionDuration),
		SessionRefresh:  true,
		CloudflareAccess: CloudflareAccessConfig{
			Enabled: getEnvBool("CF_ACCESS_ENABLED", false),
			Team:    getEnv("CF_ACCESS_TEAM", ""),
			Aud:     getEnv("CF_ACCESS_AUD", ""),
		},
	}

	// Logging configuration
	cfg.Logging = LoggingConfig{
		Level:         getEnv("LOG_LEVEL", DefaultLogLevel),
		Format:        getEnv("LOG_FORMAT", "json"),
		IncludeCaller: false,
	}

	// Display configuration
	cfg.Display = DisplayConfig{
		Timezone:       getEnv("DISPLAY_TIMEZONE", DefaultTimezone),
		DateFormat:     "Jan 2, 2006",
		TimeFormat:     "3:04 PM",
		DatetimeFormat: "Jan 2, 2006 at 3:04 PM",
	}

	// Retention configuration
	cfg.Retention = RetentionConfig{
		Enabled:               true,
		CompletedRequestsDays: getEnvInt("RETENTION_COMPLETED_DAYS", DefaultCompletedRequestsDays),
		AuditLogDays:          getEnvInt("RETENTION_AUDIT_DAYS", DefaultAuditLogDays),
		WebhookFailuresDays:   getEnvInt("RETENTION_WEBHOOK_FAILURES_DAYS", DefaultWebhookFailuresDays),
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
		return fmt.Errorf("SECRET_KEY environment variable is required")
	}
	if c.Auth.EncryptionKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY environment variable is required")
	}
	if c.Auth.AdminPassword == "" {
		return fmt.Errorf("ADMIN_PASSWORD environment variable is required")
	}

	// Validate at least one notification provider is enabled or warn
	if !c.Notifications.Ntfy.Enabled && !c.Notifications.Pushover.Enabled && !c.Notifications.Telegram.Enabled {
		// This is a warning, not an error - web UI still works
		fmt.Println("Warning: No notification providers enabled. Approvals will only be available via Web UI.")
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

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
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

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value, exists := os.LookupEnv(key); exists {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
