package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
)

// CredentialsStore manages encrypted notification provider credentials.
type CredentialsStore struct {
	db        *database.DB
	encryptor *schedcrypto.Encryptor
}

// NewCredentialsStore creates a new credentials store.
func NewCredentialsStore(db *database.DB, encryptionKey string) (*CredentialsStore, error) {
	encryptor, err := schedcrypto.NewEncryptor(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}
	return &CredentialsStore{db: db, encryptor: encryptor}, nil
}

// NtfyCredentials holds ntfy provider credentials.
type NtfyCredentials struct {
	ServerURL      string `json:"server_url"`
	Topic          string `json:"topic"`
	Token          string `json:"token,omitempty"`
	Priority       string `json:"priority,omitempty"`
	MinimalContent bool   `json:"minimal_content,omitempty"`
}

// PushoverCredentials holds Pushover provider credentials.
type PushoverCredentials struct {
	AppToken string `json:"app_token"`
	UserKey  string `json:"user_key"`
	Priority int    `json:"priority,omitempty"`
	Sound    string `json:"sound,omitempty"`
}

// TelegramCredentials holds Telegram provider credentials.
type TelegramCredentials struct {
	BotToken      string `json:"bot_token"`
	ChatID        string `json:"chat_id"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// GoogleOAuthCredentials holds Google OAuth client credentials.
type GoogleOAuthCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// WebhookCredentials holds generic webhook provider credentials.
type WebhookCredentials struct {
	URL            string `json:"url"`
	Secret         string `json:"secret,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// ProviderCredentials holds the enabled state and credentials for a provider.
type ProviderCredentials struct {
	Provider    string
	Enabled     bool
	Credentials interface{} // NtfyCredentials, PushoverCredentials, or TelegramCredentials
}

// Save stores encrypted credentials for a provider.
func (s *CredentialsStore) Save(ctx context.Context, provider string, enabled bool, credentials interface{}) error {
	credJSON, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	encrypted, err := s.encryptor.Encrypt(string(credJSON))
	if err != nil {
		return fmt.Errorf("failed to encrypt credentials: %w", err)
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notification_credentials (provider, enabled, credentials_enc, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(provider) DO UPDATE SET
			enabled = excluded.enabled,
			credentials_enc = excluded.credentials_enc,
			updated_at = datetime('now')
	`, provider, enabledInt, encrypted)

	return err
}

// Load retrieves and decrypts credentials for a provider.
func (s *CredentialsStore) Load(ctx context.Context, provider string) (*ProviderCredentials, error) {
	var enabled int
	var credEnc []byte

	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, credentials_enc FROM notification_credentials WHERE provider = ?
	`, provider).Scan(&enabled, &credEnc)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	result := &ProviderCredentials{
		Provider: provider,
		Enabled:  enabled == 1,
	}

	if credEnc == nil || len(credEnc) == 0 {
		return result, nil
	}

	decrypted, err := s.encryptor.Decrypt(credEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
	}

	switch provider {
	case "ntfy":
		var creds NtfyCredentials
		if err := json.Unmarshal([]byte(decrypted), &creds); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ntfy credentials: %w", err)
		}
		result.Credentials = &creds
	case "pushover":
		var creds PushoverCredentials
		if err := json.Unmarshal([]byte(decrypted), &creds); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pushover credentials: %w", err)
		}
		result.Credentials = &creds
	case "telegram":
		var creds TelegramCredentials
		if err := json.Unmarshal([]byte(decrypted), &creds); err != nil {
			return nil, fmt.Errorf("failed to unmarshal telegram credentials: %w", err)
		}
		result.Credentials = &creds
	case "google_oauth":
		var creds GoogleOAuthCredentials
		if err := json.Unmarshal([]byte(decrypted), &creds); err != nil {
			return nil, fmt.Errorf("failed to unmarshal google_oauth credentials: %w", err)
		}
		result.Credentials = &creds
	case "webhook":
		var creds WebhookCredentials
		if err := json.Unmarshal([]byte(decrypted), &creds); err != nil {
			return nil, fmt.Errorf("failed to unmarshal webhook credentials: %w", err)
		}
		result.Credentials = &creds
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	return result, nil
}

// LoadAll retrieves credentials for all configured providers.
func (s *CredentialsStore) LoadAll(ctx context.Context) (map[string]*ProviderCredentials, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, enabled, credentials_enc FROM notification_credentials
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*ProviderCredentials)
	for rows.Next() {
		var provider string
		var enabled int
		var credEnc []byte

		if err := rows.Scan(&provider, &enabled, &credEnc); err != nil {
			return nil, err
		}

		pc := &ProviderCredentials{
			Provider: provider,
			Enabled:  enabled == 1,
		}

		if credEnc != nil && len(credEnc) > 0 {
			decrypted, err := s.encryptor.Decrypt(credEnc)
			if err != nil {
				continue // Skip providers with decryption errors
			}

			switch provider {
			case "ntfy":
				var creds NtfyCredentials
				if json.Unmarshal([]byte(decrypted), &creds) == nil {
					pc.Credentials = &creds
				}
			case "pushover":
				var creds PushoverCredentials
				if json.Unmarshal([]byte(decrypted), &creds) == nil {
					pc.Credentials = &creds
				}
			case "telegram":
				var creds TelegramCredentials
				if json.Unmarshal([]byte(decrypted), &creds) == nil {
					pc.Credentials = &creds
				}
			case "google_oauth":
				var creds GoogleOAuthCredentials
				if json.Unmarshal([]byte(decrypted), &creds) == nil {
					pc.Credentials = &creds
				}
			case "webhook":
				var creds WebhookCredentials
				if json.Unmarshal([]byte(decrypted), &creds) == nil {
					pc.Credentials = &creds
				}
			}
		}

		result[provider] = pc
	}

	return result, rows.Err()
}

// Delete removes credentials for a provider.
func (s *CredentialsStore) Delete(ctx context.Context, provider string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notification_credentials WHERE provider = ?`, provider)
	return err
}

// IsEnabled checks if a provider is enabled in the database.
func (s *CredentialsStore) IsEnabled(ctx context.Context, provider string) bool {
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled FROM notification_credentials WHERE provider = ?
	`, provider).Scan(&enabled)
	if err != nil {
		return false
	}
	return enabled == 1
}
