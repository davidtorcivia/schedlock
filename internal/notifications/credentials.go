package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
)

// NtfyCredentials configures the ntfy provider.
type NtfyCredentials struct {
	ServerURL      string `json:"server_url"`
	Topic          string `json:"topic"`
	Token          string `json:"token,omitempty"`
	Priority       string `json:"priority,omitempty"`
	MinimalContent bool   `json:"minimal_content,omitempty"`
}

// PushoverCredentials configures the Pushover provider.
type PushoverCredentials struct {
	AppToken string `json:"app_token"`
	UserKey  string `json:"user_key"`
	Priority int    `json:"priority,omitempty"`
	Sound    string `json:"sound,omitempty"`
}

// TelegramCredentials configures the Telegram provider.
type TelegramCredentials struct {
	BotToken      string `json:"bot_token"`
	ChatID        string `json:"chat_id"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// WebhookCredentials configures the generic webhook provider.
type WebhookCredentials struct {
	URL            string `json:"url"`
	Secret         string `json:"secret,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// GoogleOAuthCredentials holds the Google OAuth client credentials.
type GoogleOAuthCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// ProviderCredentials is a provider's stored settings.
type ProviderCredentials struct {
	Provider string
	Enabled  bool
	// Credentials is one of the *Credentials types above, or nil when nothing
	// has been stored.
	Credentials any
}

// CredentialsStore persists provider credentials, encrypted at rest.
type CredentialsStore struct {
	db        *database.DB
	encryptor *schedcrypto.Encryptor
}

// NewCredentialsStore creates a credentials store.
func NewCredentialsStore(db *database.DB, encryptionKey string) (*CredentialsStore, error) {
	encryptor, err := schedcrypto.NewEncryptor(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}
	return &CredentialsStore{db: db, encryptor: encryptor}, nil
}

// Save stores a provider's credentials and enabled state.
func (s *CredentialsStore) Save(ctx context.Context, provider string, enabled bool, credentials any) error {
	credJSON, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	encrypted, err := s.encryptor.Encrypt(string(credJSON))
	if err != nil {
		return fmt.Errorf("failed to encrypt credentials: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notification_credentials (provider, enabled, credentials_enc, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(provider) DO UPDATE SET
			enabled = excluded.enabled,
			credentials_enc = excluded.credentials_enc,
			updated_at = datetime('now')
	`, provider, boolToInt(enabled), encrypted)
	return err
}

// Load reads one provider's credentials, returning (nil, nil) when none are
// stored.
func (s *CredentialsStore) Load(ctx context.Context, provider string) (*ProviderCredentials, error) {
	var (
		enabled int
		credEnc []byte
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, credentials_enc FROM notification_credentials WHERE provider = ?
	`, provider).Scan(&enabled, &credEnc)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return s.decode(provider, enabled == 1, credEnc)
}

// LoadAll reads credentials for every configured provider.
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
		var (
			provider string
			enabled  int
			credEnc  []byte
		)
		if err := rows.Scan(&provider, &enabled, &credEnc); err != nil {
			return nil, err
		}

		creds, err := s.decode(provider, enabled == 1, credEnc)
		if err != nil {
			// One unreadable provider must not hide the rest; it is reported
			// as present-but-unconfigured.
			result[provider] = &ProviderCredentials{Provider: provider, Enabled: enabled == 1}
			continue
		}
		result[provider] = creds
	}

	return result, rows.Err()
}

// LoadGoogleOAuth returns the stored Google OAuth client credentials,
// satisfying google.CredentialLoader.
func (s *CredentialsStore) LoadGoogleOAuth(ctx context.Context) (string, string, error) {
	creds, err := s.Load(ctx, ProviderGoogleOAuth)
	if err != nil {
		return "", "", err
	}
	if creds == nil {
		return "", "", errors.New("no stored Google OAuth credentials")
	}
	oauth, ok := creds.Credentials.(*GoogleOAuthCredentials)
	if !ok || oauth == nil {
		return "", "", errors.New("stored Google OAuth credentials are unreadable")
	}
	return oauth.ClientID, oauth.ClientSecret, nil
}

// Delete removes a provider's stored credentials.
func (s *CredentialsStore) Delete(ctx context.Context, provider string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notification_credentials WHERE provider = ?`, provider)
	return err
}

// decode decrypts and unmarshals a provider's credentials into its own type.
func (s *CredentialsStore) decode(provider string, enabled bool, credEnc []byte) (*ProviderCredentials, error) {
	result := &ProviderCredentials{Provider: provider, Enabled: enabled}
	if len(credEnc) == 0 {
		return result, nil
	}

	decrypted, err := s.encryptor.Decrypt(credEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt %s credentials: %w", provider, err)
	}

	var target any
	switch provider {
	case ProviderNtfy:
		target = &NtfyCredentials{}
	case ProviderPushover:
		target = &PushoverCredentials{}
	case ProviderTelegram:
		target = &TelegramCredentials{}
	case ProviderWebhook:
		target = &WebhookCredentials{}
	case ProviderGoogleOAuth:
		target = &GoogleOAuthCredentials{}
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	if err := json.Unmarshal([]byte(decrypted), target); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s credentials: %w", provider, err)
	}

	result.Credentials = target
	return result, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
