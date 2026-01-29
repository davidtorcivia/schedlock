// Package google provides Google Calendar OAuth and API integration.
package google

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// OAuthManager handles Google OAuth token management.
type OAuthManager struct {
	config    *oauth2.Config
	db        *database.DB
	encryptor *crypto.Encryptor
	mu        sync.Mutex // Serialize token refresh

	// In-memory token cache
	cachedToken *oauth2.Token
	cacheExpiry time.Time
}

// NewOAuthManager creates a new OAuth manager.
func NewOAuthManager(cfg *config.Config, db *database.DB, encryptor *crypto.Encryptor) *OAuthManager {
	oauthConfig := &oauth2.Config{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		RedirectURL:  cfg.Google.RedirectURI,
		Scopes:       cfg.Google.Scopes,
		Endpoint:     google.Endpoint,
	}

	return &OAuthManager{
		config:    oauthConfig,
		db:        db,
		encryptor: encryptor,
	}
}

// IsConfigured checks if Google OAuth is configured.
func (m *OAuthManager) IsConfigured() bool {
	return m.config.ClientID != "" && m.config.ClientSecret != ""
}

// GetAuthURL returns the OAuth authorization URL.
// For headless servers, the user should visit this URL in their browser.
func (m *OAuthManager) GetAuthURL(state string) string {
	return m.config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// GetAuthURLForHeadless returns authorization info for headless server setup.
// Returns the URL and instructions for manual code entry.
func (m *OAuthManager) GetAuthURLForHeadless(state string) HeadlessAuthInfo {
	url := m.config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	return HeadlessAuthInfo{
		AuthURL: url,
		State:   state,
		Instructions: fmt.Sprintf(`
=== Google OAuth Setup (Headless Server) ===

1. Copy this URL and open it in a browser on any device:
   %s

2. Sign in with your Google account and authorize the application.

3. After authorization, you will be redirected to a URL like:
   %s?code=AUTHORIZATION_CODE&state=%s

4. Copy the 'code' parameter value from the URL.

5. Return to the SchedLock web UI and paste the code in the authorization field.

Note: The authorization code expires after a few minutes, so complete this process promptly.
`, url, m.config.RedirectURL, state),
	}
}

// HeadlessAuthInfo contains information for headless OAuth setup.
type HeadlessAuthInfo struct {
	AuthURL      string `json:"auth_url"`
	State        string `json:"state"`
	Instructions string `json:"instructions"`
}

// ExchangeCode exchanges an authorization code for tokens.
func (m *OAuthManager) ExchangeCode(ctx context.Context, code string) error {
	token, err := m.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange code: %w", err)
	}

	// Save token to database
	if err := m.saveToken(ctx, token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	// Update cache
	m.mu.Lock()
	m.cachedToken = token
	m.cacheExpiry = token.Expiry
	m.mu.Unlock()

	util.Info("Google OAuth token saved successfully")
	return nil
}

// ExchangeCodeManual allows manual code entry for headless servers.
func (m *OAuthManager) ExchangeCodeManual(ctx context.Context, code string) error {
	return m.ExchangeCode(ctx, code)
}

// GetValidToken returns a valid OAuth token, refreshing if necessary.
func (m *OAuthManager) GetValidToken(ctx context.Context) (*oauth2.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check cache first
	if m.cachedToken != nil && time.Now().Add(5*time.Minute).Before(m.cacheExpiry) {
		return m.cachedToken, nil
	}

	// Load token from database
	token, err := m.loadToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("no OAuth token configured: %w", err)
	}

	// Check if token needs refresh (5-minute buffer)
	if token.Expiry.Before(time.Now().Add(5 * time.Minute)) {
		util.Info("Access token expired or expiring, refreshing...")

		newToken, err := m.refreshToken(ctx, token)
		if err != nil {
			// Log the failure - this is critical
			util.Error("OAuth token refresh failed", "error", err)
			return nil, fmt.Errorf("token refresh failed: %w", err)
		}
		if newToken.RefreshToken == "" {
			newToken.RefreshToken = token.RefreshToken
		}

		// Save the new token (Google may rotate refresh token)
		if err := m.saveToken(ctx, newToken); err != nil {
			util.Error("Failed to save refreshed token", "error", err)
			// Continue anyway - we have a valid token in memory
		}

		token = newToken
		util.Info("OAuth token refreshed successfully")
	}

	// Update cache
	m.cachedToken = token
	m.cacheExpiry = token.Expiry

	return token, nil
}

// refreshToken refreshes an expired token.
func (m *OAuthManager) refreshToken(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
	tokenSource := m.config.TokenSource(ctx, token)
	return tokenSource.Token()
}

// saveToken saves a token to the database (encrypted).
func (m *OAuthManager) saveToken(ctx context.Context, token *oauth2.Token) error {
	// Only store the refresh token (access tokens are ephemeral)
	if token.RefreshToken == "" {
		return fmt.Errorf("no refresh token to save")
	}

	// Encrypt the refresh token
	encryptedToken, err := m.encryptor.Encrypt(token.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Store scopes as space-separated string
	scopes := ""
	if extra := token.Extra("scope"); extra != nil {
		if s, ok := extra.(string); ok {
			scopes = s
		}
	}

	// Upsert into database
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (id, refresh_token_enc, scopes, updated_at)
		VALUES ('primary', ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			refresh_token_enc = excluded.refresh_token_enc,
			scopes = excluded.scopes,
			updated_at = datetime('now')
	`, encryptedToken, scopes)

	return err
}

// loadToken loads a token from the database.
func (m *OAuthManager) loadToken(ctx context.Context) (*oauth2.Token, error) {
	var (
		encryptedToken []byte
		scopes         sql.NullString
	)

	err := m.db.QueryRowContext(ctx, `
		SELECT refresh_token_enc, scopes
		FROM oauth_tokens
		WHERE id = 'primary'
	`).Scan(&encryptedToken, &scopes)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no OAuth token configured")
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Decrypt the refresh token
	refreshToken, err := m.encryptor.Decrypt(encryptedToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt token: %w", err)
	}

	// Create a token (will be refreshed to get access token)
	token := &oauth2.Token{
		RefreshToken: refreshToken,
		// Set expiry in the past to force refresh
		Expiry: time.Now().Add(-1 * time.Hour),
	}

	return token, nil
}

// HasToken checks if an OAuth token is configured.
func (m *OAuthManager) HasToken(ctx context.Context) bool {
	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM oauth_tokens WHERE id = 'primary'
	`).Scan(&count)

	return err == nil && count > 0
}

// IsAuthenticated checks if OAuth is configured with a valid token.
// This is a convenience wrapper around HasToken that uses a background context.
func (m *OAuthManager) IsAuthenticated() bool {
	return m.HasToken(context.Background())
}

// DeleteToken removes the stored OAuth token.
func (m *OAuthManager) DeleteToken(ctx context.Context) error {
	m.mu.Lock()
	m.cachedToken = nil
	m.cacheExpiry = time.Time{}
	m.mu.Unlock()

	_, err := m.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE id = 'primary'`)
	return err
}

// GetClient returns an HTTP client configured with OAuth credentials.
func (m *OAuthManager) GetClient(ctx context.Context) (*http.Client, error) {
	token, err := m.GetValidToken(ctx)
	if err != nil {
		return nil, err
	}
	return m.config.Client(ctx, token), nil
}

// OAuthState generates and stores a state parameter for OAuth.
type OAuthState struct {
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GenerateOAuthState creates a new OAuth state token.
func GenerateOAuthState() (string, error) {
	state, err := crypto.GenerateSessionID()
	if err != nil {
		return "", err
	}
	return state, nil
}

// StoreOAuthState stores the OAuth state in the database settings.
func (m *OAuthManager) StoreOAuthState(ctx context.Context, state string) error {
	stateData := OAuthState{
		State:     state,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}

	data, err := json.Marshal(stateData)
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

// ValidateOAuthState validates and consumes an OAuth state token.
func (m *OAuthManager) ValidateOAuthState(ctx context.Context, state string) error {
	var valueStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT value FROM settings WHERE key = 'oauth_state'
	`).Scan(&valueStr)

	if err == sql.ErrNoRows {
		return fmt.Errorf("no OAuth state found")
	}
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	var stateData OAuthState
	if err := json.Unmarshal([]byte(valueStr), &stateData); err != nil {
		return fmt.Errorf("invalid state data: %w", err)
	}

	if stateData.State != state {
		return fmt.Errorf("state mismatch")
	}

	if time.Now().After(stateData.ExpiresAt) {
		return fmt.Errorf("state expired")
	}

	// Delete the state (single-use)
	m.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'oauth_state'`)

	return nil
}
