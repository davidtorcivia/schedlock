package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// fileDuration accepts either a duration string ("30s") or a plain number of
// seconds, so a hand-written config file is forgiving about both.
type fileDuration time.Duration

func (d *fileDuration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.ScalarNode {
		return errors.New("a duration must be a scalar value")
	}

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
}

func (d fileDuration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// ConfigFile mirrors Config with pointer fields, so an absent key is
// distinguishable from a zero value and leaves the default in place.
type ConfigFile struct {
	Server        *ServerConfigFile        `yaml:"server,omitempty"`
	Database      *DatabaseConfigFile      `yaml:"database,omitempty"`
	Google        *GoogleConfigFile        `yaml:"google,omitempty"`
	Approval      *ApprovalConfigFile      `yaml:"approval,omitempty"`
	RateLimits    *RateLimitsConfigFile    `yaml:"rate_limits,omitempty"`
	Retry         *RetryConfigFile         `yaml:"retry,omitempty"`
	Notifications *NotificationsConfigFile `yaml:"notifications,omitempty"`
	Moltbot       *MoltbotConfigFile       `yaml:"moltbot,omitempty"`
	Auth          *AuthConfigFile          `yaml:"auth,omitempty"`
	Logging       *LoggingConfigFile       `yaml:"logging,omitempty"`
	Display       *DisplayConfigFile       `yaml:"display,omitempty"`
	Retention     *RetentionConfigFile     `yaml:"retention,omitempty"`
}

type ServerConfigFile struct {
	Host           *string       `yaml:"host,omitempty"`
	Port           *int          `yaml:"port,omitempty"`
	BaseURL        *string       `yaml:"base_url,omitempty"`
	ReadTimeout    *fileDuration `yaml:"read_timeout,omitempty"`
	WriteTimeout   *fileDuration `yaml:"write_timeout,omitempty"`
	IdleTimeout    *fileDuration `yaml:"idle_timeout,omitempty"`
	TrustedProxies *[]string     `yaml:"trusted_proxies,omitempty"`
}

type DatabaseConfigFile struct {
	Path *string `yaml:"path,omitempty"`
}

type GoogleConfigFile struct {
	ClientID     *string   `yaml:"client_id,omitempty"`
	ClientSecret *string   `yaml:"client_secret,omitempty"`
	RedirectURI  *string   `yaml:"redirect_uri,omitempty"`
	Scopes       *[]string `yaml:"scopes,omitempty"`
}

type ApprovalConfigFile struct {
	TimeoutMinutes *int    `yaml:"timeout_minutes,omitempty"`
	DefaultAction  *string `yaml:"default_action,omitempty"`
}

type TierLimitFile struct {
	RequestsPerMinute *int `yaml:"requests_per_minute,omitempty"`
	Burst             *int `yaml:"burst,omitempty"`
}

type RateLimitsConfigFile struct {
	Read  *TierLimitFile `yaml:"read,omitempty"`
	Write *TierLimitFile `yaml:"write,omitempty"`
	Admin *TierLimitFile `yaml:"admin,omitempty"`
}

type RetryConfigFile struct {
	Enabled              *bool  `yaml:"enabled,omitempty"`
	MaxAttempts          *int   `yaml:"max_attempts,omitempty"`
	BackoffSeconds       *[]int `yaml:"backoff_seconds,omitempty"`
	RetryableStatusCodes *[]int `yaml:"retryable_status_codes,omitempty"`
}

type NtfyConfigFile struct {
	Enabled        *bool   `yaml:"enabled,omitempty"`
	Server         *string `yaml:"server,omitempty"`
	Topic          *string `yaml:"topic,omitempty"`
	Token          *string `yaml:"token,omitempty"`
	Priority       *string `yaml:"priority,omitempty"`
	MinimalContent *bool   `yaml:"minimal_content,omitempty"`
}

type PushoverConfigFile struct {
	Enabled  *bool   `yaml:"enabled,omitempty"`
	AppToken *string `yaml:"app_token,omitempty"`
	UserKey  *string `yaml:"user_key,omitempty"`
	Priority *int    `yaml:"priority,omitempty"`
	Sound    *string `yaml:"sound,omitempty"`
}

type TelegramConfigFile struct {
	Enabled             *bool   `yaml:"enabled,omitempty"`
	BotToken            *string `yaml:"bot_token,omitempty"`
	ChatID              *string `yaml:"chat_id,omitempty"`
	WebhookSecret       *string `yaml:"webhook_secret,omitempty"`
	WebhookPath         *string `yaml:"webhook_path,omitempty"`
	AutoRegisterWebhook *bool   `yaml:"auto_register_webhook,omitempty"`
}

// GenericWebhookConfigFile configures the generic webhook provider, which had
// no representation in the config file before and could therefore only be
// enabled through the environment or the web UI.
type GenericWebhookConfigFile struct {
	Enabled        *bool   `yaml:"enabled,omitempty"`
	URL            *string `yaml:"url,omitempty"`
	Secret         *string `yaml:"secret,omitempty"`
	TimeoutSeconds *int    `yaml:"timeout_seconds,omitempty"`
}

type NotificationsConfigFile struct {
	Ntfy     *NtfyConfigFile           `yaml:"ntfy,omitempty"`
	Pushover *PushoverConfigFile       `yaml:"pushover,omitempty"`
	Telegram *TelegramConfigFile       `yaml:"telegram,omitempty"`
	Webhook  *GenericWebhookConfigFile `yaml:"webhook,omitempty"`
}

type WebhookConfigFile struct {
	Enabled          *bool     `yaml:"enabled,omitempty"`
	URL              *string   `yaml:"url,omitempty"`
	Token            *string   `yaml:"token,omitempty"`
	SessionKeyPrefix *string   `yaml:"session_key_prefix,omitempty"`
	TimeoutSeconds   *int      `yaml:"timeout_seconds,omitempty"`
	MaxRetries       *int      `yaml:"max_retries,omitempty"`
	RetryBackoff     *[]int    `yaml:"retry_backoff,omitempty"`
	NotifyOn         *[]string `yaml:"notify_on,omitempty"`
}

type MoltbotConfigFile struct {
	Webhook *WebhookConfigFile `yaml:"webhook,omitempty"`
}

type AuthConfigFile struct {
	AdminPasswordHash *string       `yaml:"admin_password_hash,omitempty"`
	AdminPassword     *string       `yaml:"admin_password,omitempty"`
	SecretKey         *string       `yaml:"secret_key,omitempty"`
	EncryptionKey     *string       `yaml:"encryption_key,omitempty"`
	SessionDuration   *fileDuration `yaml:"session_duration,omitempty"`
	SessionRefresh    *bool         `yaml:"session_refresh,omitempty"`
}

type LoggingConfigFile struct {
	Level  *string `yaml:"level,omitempty"`
	Format *string `yaml:"format,omitempty"`
}

type DisplayConfigFile struct {
	Timezone       *string `yaml:"timezone,omitempty"`
	DateFormat     *string `yaml:"date_format,omitempty"`
	TimeFormat     *string `yaml:"time_format,omitempty"`
	DatetimeFormat *string `yaml:"datetime_format,omitempty"`
}

type RetentionConfigFile struct {
	Enabled               *bool `yaml:"enabled,omitempty"`
	CompletedRequestsDays *int  `yaml:"completed_requests_days,omitempty"`
	AuditLogDays          *int  `yaml:"audit_log_days,omitempty"`
	WebhookFailuresDays   *int  `yaml:"webhook_failures_days,omitempty"`
}

// loadConfigFile reads and applies a config file if one exists.
func loadConfigFile(cfg *Config, path string) error {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	file, err := parseConfigFile(data)
	if err != nil {
		return err
	}

	applyConfigFile(cfg, file)
	return nil
}

// parseConfigFile decodes a config document, rejecting unknown keys so a typo
// is reported rather than silently ignored.
func parseConfigFile(data []byte) (*ConfigFile, error) {
	var file ConfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	// An empty document decodes to io.EOF, which is not an error here.
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	return &file, nil
}

func applyConfigFile(cfg *Config, file *ConfigFile) {
	if cfg == nil || file == nil {
		return
	}

	if f := file.Server; f != nil {
		setString(&cfg.Server.Host, f.Host)
		setInt(&cfg.Server.Port, f.Port)
		setString(&cfg.Server.BaseURL, f.BaseURL)
		setDuration(&cfg.Server.ReadTimeout, f.ReadTimeout)
		setDuration(&cfg.Server.WriteTimeout, f.WriteTimeout)
		setDuration(&cfg.Server.IdleTimeout, f.IdleTimeout)
		setStrings(&cfg.Server.TrustedProxies, f.TrustedProxies)
	}

	if f := file.Database; f != nil && f.Path != nil {
		cfg.Database.Path = filepath.Clean(*f.Path)
	}

	if f := file.Google; f != nil {
		setString(&cfg.Google.ClientID, f.ClientID)
		setString(&cfg.Google.ClientSecret, f.ClientSecret)
		setString(&cfg.Google.RedirectURI, f.RedirectURI)
		setStrings(&cfg.Google.Scopes, f.Scopes)
	}

	if f := file.Approval; f != nil {
		setInt(&cfg.Approval.TimeoutMinutes, f.TimeoutMinutes)
		setString(&cfg.Approval.DefaultAction, f.DefaultAction)
	}

	if f := file.RateLimits; f != nil {
		applyTierLimit(&cfg.RateLimits.Read, f.Read)
		applyTierLimit(&cfg.RateLimits.Write, f.Write)
		applyTierLimit(&cfg.RateLimits.Admin, f.Admin)
	}

	if f := file.Retry; f != nil {
		setBool(&cfg.Retry.Enabled, f.Enabled)
		setInt(&cfg.Retry.MaxAttempts, f.MaxAttempts)
		setInts(&cfg.Retry.BackoffSeconds, f.BackoffSeconds)
		setInts(&cfg.Retry.RetryableStatusCodes, f.RetryableStatusCodes)
	}

	if f := file.Notifications; f != nil {
		if n := f.Ntfy; n != nil {
			setBool(&cfg.Notifications.Ntfy.Enabled, n.Enabled)
			setString(&cfg.Notifications.Ntfy.Server, n.Server)
			setString(&cfg.Notifications.Ntfy.Topic, n.Topic)
			setString(&cfg.Notifications.Ntfy.Token, n.Token)
			setString(&cfg.Notifications.Ntfy.Priority, n.Priority)
			setBool(&cfg.Notifications.Ntfy.MinimalContent, n.MinimalContent)
		}
		if p := f.Pushover; p != nil {
			setBool(&cfg.Notifications.Pushover.Enabled, p.Enabled)
			setString(&cfg.Notifications.Pushover.AppToken, p.AppToken)
			setString(&cfg.Notifications.Pushover.UserKey, p.UserKey)
			setInt(&cfg.Notifications.Pushover.Priority, p.Priority)
			setString(&cfg.Notifications.Pushover.Sound, p.Sound)
		}
		if t := f.Telegram; t != nil {
			setBool(&cfg.Notifications.Telegram.Enabled, t.Enabled)
			setString(&cfg.Notifications.Telegram.BotToken, t.BotToken)
			setString(&cfg.Notifications.Telegram.ChatID, t.ChatID)
			setString(&cfg.Notifications.Telegram.WebhookSecret, t.WebhookSecret)
			setString(&cfg.Notifications.Telegram.WebhookPath, t.WebhookPath)
			setBool(&cfg.Notifications.Telegram.AutoRegisterWebhook, t.AutoRegisterWebhook)
		}
		if w := f.Webhook; w != nil {
			setBool(&cfg.Notifications.Webhook.Enabled, w.Enabled)
			setString(&cfg.Notifications.Webhook.URL, w.URL)
			setString(&cfg.Notifications.Webhook.Secret, w.Secret)
			setInt(&cfg.Notifications.Webhook.TimeoutSeconds, w.TimeoutSeconds)
		}
	}

	if file.Moltbot != nil && file.Moltbot.Webhook != nil {
		w := file.Moltbot.Webhook
		setBool(&cfg.Moltbot.Webhook.Enabled, w.Enabled)
		setString(&cfg.Moltbot.Webhook.URL, w.URL)
		setString(&cfg.Moltbot.Webhook.Token, w.Token)
		setString(&cfg.Moltbot.Webhook.SessionKeyPrefix, w.SessionKeyPrefix)
		setInt(&cfg.Moltbot.Webhook.TimeoutSeconds, w.TimeoutSeconds)
		setInt(&cfg.Moltbot.Webhook.MaxRetries, w.MaxRetries)
		setInts(&cfg.Moltbot.Webhook.RetryBackoff, w.RetryBackoff)
		setStrings(&cfg.Moltbot.Webhook.NotifyOn, w.NotifyOn)
	}

	if f := file.Auth; f != nil {
		setString(&cfg.Auth.AdminPasswordHash, f.AdminPasswordHash)
		setString(&cfg.Auth.AdminPassword, f.AdminPassword)
		setString(&cfg.Auth.SecretKey, f.SecretKey)
		setString(&cfg.Auth.EncryptionKey, f.EncryptionKey)
		setDuration(&cfg.Auth.SessionDuration, f.SessionDuration)
		setBool(&cfg.Auth.SessionRefresh, f.SessionRefresh)
	}

	if f := file.Logging; f != nil {
		setString(&cfg.Logging.Level, f.Level)
		setString(&cfg.Logging.Format, f.Format)
	}

	if f := file.Display; f != nil {
		setString(&cfg.Display.Timezone, f.Timezone)
		setString(&cfg.Display.DateFormat, f.DateFormat)
		setString(&cfg.Display.TimeFormat, f.TimeFormat)
		setString(&cfg.Display.DatetimeFormat, f.DatetimeFormat)
	}

	if f := file.Retention; f != nil {
		setBool(&cfg.Retention.Enabled, f.Enabled)
		setInt(&cfg.Retention.CompletedRequestsDays, f.CompletedRequestsDays)
		setInt(&cfg.Retention.AuditLogDays, f.AuditLogDays)
		setInt(&cfg.Retention.WebhookFailuresDays, f.WebhookFailuresDays)
	}
}

func applyTierLimit(limit *TierLimit, file *TierLimitFile) {
	if limit == nil || file == nil {
		return
	}
	setInt(&limit.RequestsPerMinute, file.RequestsPerMinute)
	setInt(&limit.Burst, file.Burst)
}

func setString(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

func setInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

func setBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func setInts(dst *[]int, src *[]int) {
	if src != nil {
		*dst = *src
	}
}

func setStrings(dst *[]string, src *[]string) {
	if src != nil {
		*dst = *src
	}
}

func setDuration(dst *time.Duration, src *fileDuration) {
	if src != nil {
		*dst = time.Duration(*src)
	}
}

// SaveConfigFile writes the settings the setup wizard collects.
//
// The existing file is read first and only the managed keys are replaced, so
// running setup (or re-running it) never discards notification, retention, or
// rate-limit settings an operator has written by hand.
func SaveConfigFile(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("failed to create the config directory: %w", err)
	}

	file := &ConfigFile{}
	if existing, err := os.ReadFile(path); err == nil {
		if parsed, parseErr := parseConfigFile(existing); parseErr == nil {
			file = parsed
		} else {
			return fmt.Errorf("refusing to overwrite an unreadable config file: %w", parseErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read the existing config file: %w", err)
	}

	if file.Server == nil {
		file.Server = &ServerConfigFile{}
	}
	if cfg.Server.BaseURL != "" {
		file.Server.BaseURL = &cfg.Server.BaseURL
	}

	if file.Auth == nil {
		file.Auth = &AuthConfigFile{}
	}
	if cfg.Auth.AdminPasswordHash != "" {
		file.Auth.AdminPasswordHash = &cfg.Auth.AdminPasswordHash
	}
	if cfg.Auth.SecretKey != "" {
		file.Auth.SecretKey = &cfg.Auth.SecretKey
	}
	if cfg.Auth.EncryptionKey != "" {
		file.Auth.EncryptionKey = &cfg.Auth.EncryptionKey
	}

	if cfg.Google.ClientID != "" || cfg.Google.ClientSecret != "" {
		if file.Google == nil {
			file.Google = &GoogleConfigFile{}
		}
		if cfg.Google.ClientID != "" {
			file.Google.ClientID = &cfg.Google.ClientID
		}
		if cfg.Google.ClientSecret != "" {
			file.Google.ClientSecret = &cfg.Google.ClientSecret
		}
	}

	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("failed to encode the configuration: %w", err)
	}

	header := []byte("# SchedLock configuration\n" +
		"# This file holds secrets. Keep it readable only by the service account.\n\n")

	// Write to a temporary file and rename, so an interrupted write cannot
	// leave a half-written configuration (and with it, an unrecoverable
	// encryption key) behind.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(header, data...), 0o600); err != nil {
		return fmt.Errorf("failed to write the config file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to replace the config file: %w", err)
	}

	return nil
}

// GetConfigFilePath returns the configured config file location.
func GetConfigFilePath() string {
	dataDir := envString(DefaultDataDir, "SCHEDLOCK_DATA_DIR", "DATA_DIR")
	return envString(filepath.Join(dataDir, "config.yaml"), "SCHEDLOCK_CONFIG_FILE", "CONFIG_FILE")
}
