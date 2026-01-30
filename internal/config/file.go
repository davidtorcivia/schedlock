package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type fileDuration time.Duration

func (d *fileDuration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!int" {
			var seconds int64
			if err := value.Decode(&seconds); err != nil {
				return err
			}
			*d = fileDuration(time.Duration(seconds) * time.Second)
			return nil
		}
		var raw string
		if err := value.Decode(&raw); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", raw, err)
		}
		*d = fileDuration(parsed)
		return nil
	default:
		return fmt.Errorf("invalid duration type")
	}
}

type ConfigFile struct {
	Server        *ServerConfigFile        `yaml:"server"`
	Database      *DatabaseConfigFile      `yaml:"database"`
	Google        *GoogleConfigFile        `yaml:"google"`
	Approval      *ApprovalConfigFile      `yaml:"approval"`
	RateLimits    *RateLimitsConfigFile    `yaml:"rate_limits"`
	Retry         *RetryConfigFile         `yaml:"retry"`
	Notifications *NotificationsConfigFile `yaml:"notifications"`
	Moltbot       *MoltbotConfigFile       `yaml:"moltbot"`
	Auth          *AuthConfigFile          `yaml:"auth"`
	Logging       *LoggingConfigFile       `yaml:"logging"`
	Display       *DisplayConfigFile       `yaml:"display"`
	Retention     *RetentionConfigFile     `yaml:"retention"`
}

type ServerConfigFile struct {
	Host         *string       `yaml:"host"`
	Port         *int          `yaml:"port"`
	BaseURL      *string       `yaml:"base_url"`
	ReadTimeout  *fileDuration `yaml:"read_timeout"`
	WriteTimeout *fileDuration `yaml:"write_timeout"`
}

type DatabaseConfigFile struct {
	Path          *string `yaml:"path"`
	WALMode       *bool   `yaml:"wal_mode"`
	BusyTimeoutMs *int    `yaml:"busy_timeout_ms"`
}

type GoogleConfigFile struct {
	ClientID     *string   `yaml:"client_id"`
	ClientSecret *string   `yaml:"client_secret"`
	RedirectURI  *string   `yaml:"redirect_uri"`
	Scopes       *[]string `yaml:"scopes"`
}

type ApprovalConfigFile struct {
	TimeoutMinutes *int    `yaml:"timeout_minutes"`
	DefaultAction  *string `yaml:"default_action"`
}

type TierLimitFile struct {
	RequestsPerMinute *int `yaml:"requests_per_minute"`
	Burst             *int `yaml:"burst"`
}

type RateLimitsConfigFile struct {
	Read  *TierLimitFile `yaml:"read"`
	Write *TierLimitFile `yaml:"write"`
	Admin *TierLimitFile `yaml:"admin"`
}

type RetryConfigFile struct {
	Enabled              *bool  `yaml:"enabled"`
	MaxAttempts          *int   `yaml:"max_attempts"`
	BackoffSeconds       *[]int `yaml:"backoff_seconds"`
	RetryableStatusCodes *[]int `yaml:"retryable_status_codes"`
}

type NtfyConfigFile struct {
	Enabled        *bool   `yaml:"enabled"`
	Server         *string `yaml:"server"`
	Topic          *string `yaml:"topic"`
	Token          *string `yaml:"token"`
	Priority       *string `yaml:"priority"`
	MinimalContent *bool   `yaml:"minimal_content"`
}

type PushoverConfigFile struct {
	Enabled  *bool   `yaml:"enabled"`
	AppToken *string `yaml:"app_token"`
	UserKey  *string `yaml:"user_key"`
	Priority *int    `yaml:"priority"`
	Sound    *string `yaml:"sound"`
}

type TelegramConfigFile struct {
	Enabled             *bool   `yaml:"enabled"`
	BotToken            *string `yaml:"bot_token"`
	ChatID              *string `yaml:"chat_id"`
	WebhookSecret       *string `yaml:"webhook_secret"`
	WebhookPath         *string `yaml:"webhook_path"`
	AutoRegisterWebhook *bool   `yaml:"auto_register_webhook"`
}

type NotificationsConfigFile struct {
	Ntfy     *NtfyConfigFile     `yaml:"ntfy"`
	Pushover *PushoverConfigFile `yaml:"pushover"`
	Telegram *TelegramConfigFile `yaml:"telegram"`
}

type WebhookConfigFile struct {
	Enabled          *bool     `yaml:"enabled"`
	URL              *string   `yaml:"url"`
	Token            *string   `yaml:"token"`
	SessionKeyPrefix *string   `yaml:"session_key_prefix"`
	TimeoutSeconds   *int      `yaml:"timeout_seconds"`
	MaxRetries       *int      `yaml:"max_retries"`
	RetryBackoff     *[]int    `yaml:"retry_backoff"`
	NotifyOn         *[]string `yaml:"notify_on"`
}

type MoltbotConfigFile struct {
	Webhook *WebhookConfigFile `yaml:"webhook"`
}

type CloudflareAccessConfigFile struct {
	Enabled *bool   `yaml:"enabled"`
	Team    *string `yaml:"team"`
	Aud     *string `yaml:"aud"`
}

type AuthConfigFile struct {
	AdminPasswordHash *string                     `yaml:"admin_password_hash"`
	AdminPassword     *string                     `yaml:"admin_password"`
	SecretKey         *string                     `yaml:"secret_key"`
	EncryptionKey     *string                     `yaml:"encryption_key"`
	SessionDuration   *fileDuration               `yaml:"session_duration"`
	SessionRefresh    *bool                       `yaml:"session_refresh"`
	CloudflareAccess  *CloudflareAccessConfigFile `yaml:"cloudflare_access"`
}

type LoggingConfigFile struct {
	Level         *string `yaml:"level"`
	Format        *string `yaml:"format"`
	IncludeCaller *bool   `yaml:"include_caller"`
}

type DisplayConfigFile struct {
	Timezone       *string `yaml:"timezone"`
	DateFormat     *string `yaml:"date_format"`
	TimeFormat     *string `yaml:"time_format"`
	DatetimeFormat *string `yaml:"datetime_format"`
}

type RetentionConfigFile struct {
	Enabled               *bool   `yaml:"enabled"`
	CompletedRequestsDays *int    `yaml:"completed_requests_days"`
	AuditLogDays          *int    `yaml:"audit_log_days"`
	WebhookFailuresDays   *int    `yaml:"webhook_failures_days"`
	VacuumSchedule        *string `yaml:"vacuum_schedule"`
}

func loadConfigFile(cfg *Config, path string) error {
	if path == "" {
		return nil
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var file ConfigFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	applyConfigFile(cfg, &file)
	return nil
}

func applyConfigFile(cfg *Config, file *ConfigFile) {
	if cfg == nil || file == nil {
		return
	}

	if file.Server != nil {
		if file.Server.Host != nil {
			cfg.Server.Host = *file.Server.Host
		}
		if file.Server.Port != nil {
			cfg.Server.Port = *file.Server.Port
		}
		if file.Server.BaseURL != nil {
			cfg.Server.BaseURL = *file.Server.BaseURL
		}
		if file.Server.ReadTimeout != nil {
			cfg.Server.ReadTimeout = time.Duration(*file.Server.ReadTimeout)
		}
		if file.Server.WriteTimeout != nil {
			cfg.Server.WriteTimeout = time.Duration(*file.Server.WriteTimeout)
		}
	}

	if file.Database != nil {
		if file.Database.Path != nil {
			cfg.Database.Path = filepath.Clean(*file.Database.Path)
		}
		if file.Database.WALMode != nil {
			cfg.Database.WALMode = *file.Database.WALMode
		}
		if file.Database.BusyTimeoutMs != nil {
			cfg.Database.BusyTimeoutMs = *file.Database.BusyTimeoutMs
		}
	}

	if file.Google != nil {
		if file.Google.ClientID != nil {
			cfg.Google.ClientID = *file.Google.ClientID
		}
		if file.Google.ClientSecret != nil {
			cfg.Google.ClientSecret = *file.Google.ClientSecret
		}
		if file.Google.RedirectURI != nil {
			cfg.Google.RedirectURI = *file.Google.RedirectURI
		}
		if file.Google.Scopes != nil {
			cfg.Google.Scopes = *file.Google.Scopes
		}
	}

	if file.Approval != nil {
		if file.Approval.TimeoutMinutes != nil {
			cfg.Approval.TimeoutMinutes = *file.Approval.TimeoutMinutes
		}
		if file.Approval.DefaultAction != nil {
			cfg.Approval.DefaultAction = *file.Approval.DefaultAction
		}
	}

	if file.RateLimits != nil {
		applyTierLimitFile(&cfg.RateLimits.Read, file.RateLimits.Read)
		applyTierLimitFile(&cfg.RateLimits.Write, file.RateLimits.Write)
		applyTierLimitFile(&cfg.RateLimits.Admin, file.RateLimits.Admin)
	}

	if file.Retry != nil {
		if file.Retry.Enabled != nil {
			cfg.Retry.Enabled = *file.Retry.Enabled
		}
		if file.Retry.MaxAttempts != nil {
			cfg.Retry.MaxAttempts = *file.Retry.MaxAttempts
		}
		if file.Retry.BackoffSeconds != nil {
			cfg.Retry.BackoffSeconds = *file.Retry.BackoffSeconds
		}
		if file.Retry.RetryableStatusCodes != nil {
			cfg.Retry.RetryableStatusCodes = *file.Retry.RetryableStatusCodes
		}
	}

	if file.Notifications != nil {
		if file.Notifications.Ntfy != nil {
			if file.Notifications.Ntfy.Enabled != nil {
				cfg.Notifications.Ntfy.Enabled = *file.Notifications.Ntfy.Enabled
			}
			if file.Notifications.Ntfy.Server != nil {
				cfg.Notifications.Ntfy.Server = *file.Notifications.Ntfy.Server
			}
			if file.Notifications.Ntfy.Topic != nil {
				cfg.Notifications.Ntfy.Topic = *file.Notifications.Ntfy.Topic
			}
			if file.Notifications.Ntfy.Token != nil {
				cfg.Notifications.Ntfy.Token = *file.Notifications.Ntfy.Token
			}
			if file.Notifications.Ntfy.Priority != nil {
				cfg.Notifications.Ntfy.Priority = *file.Notifications.Ntfy.Priority
			}
			if file.Notifications.Ntfy.MinimalContent != nil {
				cfg.Notifications.Ntfy.MinimalContent = *file.Notifications.Ntfy.MinimalContent
			}
		}
		if file.Notifications.Pushover != nil {
			if file.Notifications.Pushover.Enabled != nil {
				cfg.Notifications.Pushover.Enabled = *file.Notifications.Pushover.Enabled
			}
			if file.Notifications.Pushover.AppToken != nil {
				cfg.Notifications.Pushover.AppToken = *file.Notifications.Pushover.AppToken
			}
			if file.Notifications.Pushover.UserKey != nil {
				cfg.Notifications.Pushover.UserKey = *file.Notifications.Pushover.UserKey
			}
			if file.Notifications.Pushover.Priority != nil {
				cfg.Notifications.Pushover.Priority = *file.Notifications.Pushover.Priority
			}
			if file.Notifications.Pushover.Sound != nil {
				cfg.Notifications.Pushover.Sound = *file.Notifications.Pushover.Sound
			}
		}
		if file.Notifications.Telegram != nil {
			if file.Notifications.Telegram.Enabled != nil {
				cfg.Notifications.Telegram.Enabled = *file.Notifications.Telegram.Enabled
			}
			if file.Notifications.Telegram.BotToken != nil {
				cfg.Notifications.Telegram.BotToken = *file.Notifications.Telegram.BotToken
			}
			if file.Notifications.Telegram.ChatID != nil {
				cfg.Notifications.Telegram.ChatID = *file.Notifications.Telegram.ChatID
			}
			if file.Notifications.Telegram.WebhookSecret != nil {
				cfg.Notifications.Telegram.WebhookSecret = *file.Notifications.Telegram.WebhookSecret
			}
			if file.Notifications.Telegram.WebhookPath != nil {
				cfg.Notifications.Telegram.WebhookPath = *file.Notifications.Telegram.WebhookPath
			}
			if file.Notifications.Telegram.AutoRegisterWebhook != nil {
				cfg.Notifications.Telegram.AutoRegisterWebhook = *file.Notifications.Telegram.AutoRegisterWebhook
			}
		}
	}

	if file.Moltbot != nil && file.Moltbot.Webhook != nil {
		w := file.Moltbot.Webhook
		if w.Enabled != nil {
			cfg.Moltbot.Webhook.Enabled = *w.Enabled
		}
		if w.URL != nil {
			cfg.Moltbot.Webhook.URL = *w.URL
		}
		if w.Token != nil {
			cfg.Moltbot.Webhook.Token = *w.Token
		}
		if w.SessionKeyPrefix != nil {
			cfg.Moltbot.Webhook.SessionKeyPrefix = *w.SessionKeyPrefix
		}
		if w.TimeoutSeconds != nil {
			cfg.Moltbot.Webhook.TimeoutSeconds = *w.TimeoutSeconds
		}
		if w.MaxRetries != nil {
			cfg.Moltbot.Webhook.MaxRetries = *w.MaxRetries
		}
		if w.RetryBackoff != nil {
			cfg.Moltbot.Webhook.RetryBackoff = *w.RetryBackoff
		}
		if w.NotifyOn != nil {
			cfg.Moltbot.Webhook.NotifyOn = *w.NotifyOn
		}
	}

	if file.Auth != nil {
		if file.Auth.AdminPasswordHash != nil {
			cfg.Auth.AdminPasswordHash = *file.Auth.AdminPasswordHash
		}
		if file.Auth.AdminPassword != nil {
			cfg.Auth.AdminPassword = *file.Auth.AdminPassword
		}
		if file.Auth.SecretKey != nil {
			cfg.Auth.SecretKey = *file.Auth.SecretKey
		}
		if file.Auth.EncryptionKey != nil {
			cfg.Auth.EncryptionKey = *file.Auth.EncryptionKey
		}
		if file.Auth.SessionDuration != nil {
			cfg.Auth.SessionDuration = time.Duration(*file.Auth.SessionDuration)
		}
		if file.Auth.SessionRefresh != nil {
			cfg.Auth.SessionRefresh = *file.Auth.SessionRefresh
		}
		if file.Auth.CloudflareAccess != nil {
			if file.Auth.CloudflareAccess.Enabled != nil {
				cfg.Auth.CloudflareAccess.Enabled = *file.Auth.CloudflareAccess.Enabled
			}
			if file.Auth.CloudflareAccess.Team != nil {
				cfg.Auth.CloudflareAccess.Team = *file.Auth.CloudflareAccess.Team
			}
			if file.Auth.CloudflareAccess.Aud != nil {
				cfg.Auth.CloudflareAccess.Aud = *file.Auth.CloudflareAccess.Aud
			}
		}
	}

	if file.Logging != nil {
		if file.Logging.Level != nil {
			cfg.Logging.Level = *file.Logging.Level
		}
		if file.Logging.Format != nil {
			cfg.Logging.Format = *file.Logging.Format
		}
		if file.Logging.IncludeCaller != nil {
			cfg.Logging.IncludeCaller = *file.Logging.IncludeCaller
		}
	}

	if file.Display != nil {
		if file.Display.Timezone != nil {
			cfg.Display.Timezone = *file.Display.Timezone
		}
		if file.Display.DateFormat != nil {
			cfg.Display.DateFormat = *file.Display.DateFormat
		}
		if file.Display.TimeFormat != nil {
			cfg.Display.TimeFormat = *file.Display.TimeFormat
		}
		if file.Display.DatetimeFormat != nil {
			cfg.Display.DatetimeFormat = *file.Display.DatetimeFormat
		}
	}

	if file.Retention != nil {
		if file.Retention.Enabled != nil {
			cfg.Retention.Enabled = *file.Retention.Enabled
		}
		if file.Retention.CompletedRequestsDays != nil {
			cfg.Retention.CompletedRequestsDays = *file.Retention.CompletedRequestsDays
		}
		if file.Retention.AuditLogDays != nil {
			cfg.Retention.AuditLogDays = *file.Retention.AuditLogDays
		}
		if file.Retention.WebhookFailuresDays != nil {
			cfg.Retention.WebhookFailuresDays = *file.Retention.WebhookFailuresDays
		}
		if file.Retention.VacuumSchedule != nil {
			cfg.Retention.VacuumSchedule = *file.Retention.VacuumSchedule
		}
	}
}

func applyTierLimitFile(limit *TierLimit, file *TierLimitFile) {
	if limit == nil || file == nil {
		return
	}
	if file.RequestsPerMinute != nil {
		limit.RequestsPerMinute = *file.RequestsPerMinute
	}
	if file.Burst != nil {
		limit.Burst = *file.Burst
	}
}

// SaveConfigFile writes the configuration to a YAML file.
func SaveConfigFile(cfg *Config, path string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Build the file structure with only non-empty values
	file := ConfigFile{}

	// Server config
	if cfg.Server.BaseURL != "" {
		file.Server = &ServerConfigFile{
			BaseURL: &cfg.Server.BaseURL,
		}
	}

	// Auth config (required fields)
	file.Auth = &AuthConfigFile{}
	if cfg.Auth.AdminPasswordHash != "" {
		file.Auth.AdminPasswordHash = &cfg.Auth.AdminPasswordHash
	}
	if cfg.Auth.SecretKey != "" {
		file.Auth.SecretKey = &cfg.Auth.SecretKey
	}
	if cfg.Auth.EncryptionKey != "" {
		file.Auth.EncryptionKey = &cfg.Auth.EncryptionKey
	}

	// Google config (if configured)
	if cfg.Google.ClientID != "" || cfg.Google.ClientSecret != "" {
		file.Google = &GoogleConfigFile{}
		if cfg.Google.ClientID != "" {
			file.Google.ClientID = &cfg.Google.ClientID
		}
		if cfg.Google.ClientSecret != "" {
			file.Google.ClientSecret = &cfg.Google.ClientSecret
		}
	}

	data, err := yaml.Marshal(&file)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Add a header comment
	header := []byte("# SchedLock Configuration\n# Generated by setup wizard\n\n")
	data = append(header, data...)

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetConfigFilePath returns the path to the config file based on environment variables.
func GetConfigFilePath() string {
	dataDir := getEnvAnyDefault(DefaultDataDir, "SCHEDLOCK_DATA_DIR", "DATA_DIR")
	return getEnvAnyDefault(filepath.Join(dataDir, "config.yaml"), "SCHEDLOCK_CONFIG_FILE", "CONFIG_FILE")
}
