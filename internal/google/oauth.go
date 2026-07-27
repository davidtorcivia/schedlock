// Package google provides Google OAuth token management.
package google

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Errors reported by the OAuth manager.
var (
	ErrNotConfigured  = errors.New("google OAuth credentials are not configured")
	ErrNoToken        = errors.New("google Calendar is not connected")
	ErrStateNotFound  = errors.New("no pending authorization")
	ErrStateMismatch  = errors.New("authorization state does not match")
	ErrStateExpired   = errors.New("authorization request expired; please try again")
	oauthStateTimeout = 10 * time.Minute
)

// refreshLeeway is how long before expiry an access token is refreshed.
const refreshLeeway = 5 * time.Minute

// CredentialLoader supplies OAuth client credentials stored at runtime.
type CredentialLoader interface {
	LoadGoogleOAuth(ctx context.Context) (clientID, clientSecret string, err error)
}

// OAuthManager owns the Google OAuth client configuration and the stored
// refresh token.
type OAuthManager struct {
	db        *database.DB
	encryptor *crypto.Encryptor
	credStore CredentialLoader

	// mu guards every field below. Credentials and the base URL are mutated at
	// runtime from the settings page while requests are reading them.
	mu          sync.Mutex
	oauthConfig *oauth2.Config
	baseURL     string
	scopes      []string
	cachedToken *oauth2.Token
}

// NewOAuthManager creates an OAuth manager from static configuration.
func NewOAuthManager(cfg *config.Config, db *database.DB, encryptor *crypto.Encryptor) *OAuthManager {
	m := &OAuthManager{
		db:        db,
		encryptor: encryptor,
		baseURL:   cfg.Server.BaseURL,
		scopes:    cfg.Google.Scopes,
	}
	m.oauthConfig = m.buildConfig(cfg.Google.ClientID, cfg.Google.ClientSecret, cfg.Google.RedirectURI)
	return m
}

// SetCredentialStore attaches the runtime credential store.
func (m *OAuthManager) SetCredentialStore(store CredentialLoader) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credStore = store
}

// buildConfig assembles an oauth2.Config. Callers must hold mu, or be the
// constructor.
func (m *OAuthManager) buildConfig(clientID, clientSecret, redirectURI string) *oauth2.Config {
	if redirectURI == "" {
		redirectURI = m.baseURL + "/oauth/callback"
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       m.scopes,
		Endpoint:     googleoauth.Endpoint,
	}
}

// UpdateCredentials replaces the OAuth client credentials.
func (m *OAuthManager) UpdateCredentials(clientID, clientSecret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCredentialsLocked(clientID, clientSecret)
}

func (m *OAuthManager) setCredentialsLocked(clientID, clientSecret string) {
	m.oauthConfig = m.buildConfig(clientID, clientSecret, "")
	// Credentials changed, so any cached access token was minted by a
	// different client and must not be reused.
	m.cachedToken = nil
}

// UpdateBaseURL updates the public base URL and the derived redirect URI.
func (m *OAuthManager) UpdateBaseURL(baseURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baseURL = baseURL
	m.oauthConfig = m.buildConfig(m.oauthConfig.ClientID, m.oauthConfig.ClientSecret, "")
}

// currentConfig returns the OAuth config, loading credentials from the store
// on first use if the static configuration was empty.
func (m *OAuthManager) currentConfig(ctx context.Context) (*oauth2.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentConfigLocked(ctx)
}

func (m *OAuthManager) currentConfigLocked(ctx context.Context) (*oauth2.Config, error) {
	if m.oauthConfig.ClientID != "" && m.oauthConfig.ClientSecret != "" {
		return m.oauthConfig, nil
	}

	if m.credStore != nil {
		clientID, clientSecret, err := m.credStore.LoadGoogleOAuth(ctx)
		if err == nil && clientID != "" && clientSecret != "" {
			m.setCredentialsLocked(clientID, clientSecret)
			return m.oauthConfig, nil
		}
	}

	return nil, ErrNotConfigured
}

// IsConfigured reports whether client credentials are available.
func (m *OAuthManager) IsConfigured(ctx context.Context) bool {
	_, err := m.currentConfig(ctx)
	return err == nil
}

// RedirectURI returns the redirect URI Google must be configured with.
func (m *OAuthManager) RedirectURI() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.oauthConfig.RedirectURL
}

// AuthCodeURL returns the URL the operator visits to authorize access.
func (m *OAuthManager) AuthCodeURL(ctx context.Context, state string) (string, error) {
	cfg, err := m.currentConfig(ctx)
	if err != nil {
		return "", err
	}
	// AccessTypeOffline plus ApprovalForce guarantees a refresh token even when
	// the account has authorized this client before.
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), nil
}

// ExchangeCode trades an authorization code for tokens and stores the refresh
// token.
func (m *OAuthManager) ExchangeCode(ctx context.Context, code string) error {
	cfg, err := m.currentConfig(ctx)
	if err != nil {
		return err
	}

	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return errors.New("google did not return a refresh token; revoke the app's access and try again")
	}

	if err := m.saveToken(ctx, token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	m.mu.Lock()
	m.cachedToken = token
	m.mu.Unlock()

	util.Info("Google OAuth token stored")
	return nil
}

// tokenSource returns a token source backed by the stored refresh token.
//
// The oauth2 library refreshes lazily and caches internally; a single source is
// reused so concurrent calendar calls share one refresh rather than each
// minting their own access token.
func (m *OAuthManager) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	m.mu.Lock()
	cfg, err := m.currentConfigLocked(ctx)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	cached := m.cachedToken
	m.mu.Unlock()

	if cached != nil && cached.Valid() && time.Until(cached.Expiry) > refreshLeeway {
		return oauth2.StaticTokenSource(cached), nil
	}

	stored, err := m.loadToken(ctx)
	if err != nil {
		return nil, err
	}

	return &persistingTokenSource{
		manager: m,
		source:  cfg.TokenSource(ctx, stored),
		refresh: stored.RefreshToken,
	}, nil
}

// persistingTokenSource writes rotated refresh tokens back to storage.
//
// Google may hand back a new refresh token during a refresh; dropping it would
// leave the stored credential stale and eventually break calendar access.
type persistingTokenSource struct {
	manager *OAuthManager
	source  oauth2.TokenSource
	refresh string
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := p.source.Token()
	if err != nil {
		util.Error("OAuth token refresh failed", "error", err)
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}

	if token.RefreshToken == "" {
		token.RefreshToken = p.refresh
	}

	p.manager.mu.Lock()
	p.manager.cachedToken = token
	p.manager.mu.Unlock()

	if token.RefreshToken != p.refresh {
		if err := p.manager.saveToken(context.Background(), token); err != nil {
			util.Error("Failed to persist rotated refresh token", "error", err)
		} else {
			p.refresh = token.RefreshToken
		}
	}

	return token, nil
}

// Client returns an HTTP client that authenticates as the connected account.
func (m *OAuthManager) Client(ctx context.Context) (*http.Client, error) {
	source, err := m.tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, source), nil
}

// saveToken stores the refresh token, encrypted.
func (m *OAuthManager) saveToken(ctx context.Context, token *oauth2.Token) error {
	if token.RefreshToken == "" {
		return errors.New("no refresh token to save")
	}

	encrypted, err := m.encryptor.Encrypt(token.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	scopes := ""
	if raw, ok := token.Extra("scope").(string); ok {
		scopes = raw
	}

	// An empty scope string during a refresh must not erase what was recorded
	// at authorization time.
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (id, refresh_token_enc, scopes, updated_at)
		VALUES ('primary', ?, NULLIF(?, ''), datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			refresh_token_enc = excluded.refresh_token_enc,
			scopes = COALESCE(excluded.scopes, oauth_tokens.scopes),
			updated_at = datetime('now')
	`, encrypted, scopes)
	return err
}

// loadToken reads and decrypts the stored refresh token.
func (m *OAuthManager) loadToken(ctx context.Context) (*oauth2.Token, error) {
	var encrypted []byte
	err := m.db.QueryRowContext(ctx, `
		SELECT refresh_token_enc FROM oauth_tokens WHERE id = 'primary'
	`).Scan(&encrypted)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoToken
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	refreshToken, err := m.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt stored token: %w", err)
	}

	// The expiry is set in the past so the first use always refreshes, which is
	// also how a fresh access token is obtained after a restart.
	return &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-time.Hour),
	}, nil
}

// HasToken reports whether Google Calendar has been connected.
func (m *OAuthManager) HasToken(ctx context.Context) bool {
	var count int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_tokens WHERE id = 'primary'`).Scan(&count)
	return err == nil && count > 0
}

// DeleteToken disconnects the calendar account.
func (m *OAuthManager) DeleteToken(ctx context.Context) error {
	m.mu.Lock()
	m.cachedToken = nil
	m.mu.Unlock()

	_, err := m.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE id = 'primary'`)
	return err
}

// oauthState is the stored CSRF state for an in-flight authorization.
type oauthState struct {
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GenerateOAuthState creates an unguessable state parameter.
func GenerateOAuthState() (string, error) {
	return crypto.GenerateSessionID()
}

// StoreOAuthState records the state expected on the callback.
func (m *OAuthManager) StoreOAuthState(ctx context.Context, state string) error {
	data, err := json.Marshal(oauthState{
		State:     state,
		ExpiresAt: time.Now().Add(oauthStateTimeout),
	})
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ('oauth_state', ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
	`, string(data))
	return err
}

// ValidateOAuthState checks and consumes the stored state, so an authorization
// code cannot be replayed and a forged callback cannot bind an attacker's
// account.
func (m *OAuthManager) ValidateOAuthState(ctx context.Context, state string) error {
	var raw string
	err := m.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'oauth_state'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStateNotFound
	}
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	var stored oauthState
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return fmt.Errorf("invalid stored state: %w", err)
	}

	// The state is single-use whatever the outcome.
	if _, err := m.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'oauth_state'`); err != nil {
		util.Warn("Failed to clear OAuth state", "error", err)
	}

	if subtle.ConstantTimeCompare([]byte(stored.State), []byte(state)) != 1 {
		return ErrStateMismatch
	}
	if time.Now().After(stored.ExpiresAt) {
		return ErrStateExpired
	}
	return nil
}
