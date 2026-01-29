// Package web provides web UI handlers and session management.
package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

const (
	sessionCookieName = "schedlock_session"
	csrfCookieName    = "schedlock_csrf"
	sessionDuration   = 24 * time.Hour
)

// SessionManager handles web UI sessions.
type SessionManager struct {
	db     *database.DB
	config *config.AuthConfig
}

// NewSessionManager creates a new session manager.
func NewSessionManager(db *database.DB, cfg *config.AuthConfig) *SessionManager {
	return &SessionManager{db: db, config: cfg}
}

// Session represents a web UI session.
type Session struct {
	ID        string
	UserID    string
	IPAddress string
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateSession creates a new session for a user.
func (m *SessionManager) CreateSession(ctx context.Context, userID, ipAddress, userAgent string) (*Session, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(sessionDuration)

	_, err = m.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, ip_address, user_agent, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, userID, ipAddress, userAgent, expiresAt.Format(time.RFC3339))

	if err != nil {
		return nil, err
	}

	return &Session{
		ID:        sessionID,
		UserID:    userID,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}, nil
}

// ValidateSession checks if a session is valid.
func (m *SessionManager) ValidateSession(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	var createdAt, expiresAt string

	err := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, ip_address, user_agent, created_at, expires_at
		FROM sessions
		WHERE id = ? AND expires_at > datetime('now')
	`, sessionID).Scan(&session.ID, &session.UserID, &session.IPAddress, &session.UserAgent, &createdAt, &expiresAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	session.CreatedAt, _ = util.ParseSQLiteTimestamp(createdAt)
	session.ExpiresAt, _ = util.ParseSQLiteTimestamp(expiresAt)

	return &session, nil
}

// DeleteSession removes a session.
func (m *SessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// RefreshSession extends a session's expiration.
func (m *SessionManager) RefreshSession(ctx context.Context, sessionID string) error {
	expiresAt := time.Now().Add(sessionDuration)
	_, err := m.db.ExecContext(ctx, `
		UPDATE sessions SET expires_at = ? WHERE id = ?
	`, expiresAt.Format(time.RFC3339), sessionID)
	return err
}

// VerifyPassword checks if a password matches the admin password.
func (m *SessionManager) VerifyPassword(password string) bool {
	if m.config.AdminPassword == "" {
		return false
	}
	// For simplicity, do direct comparison. In production, store a hashed version.
	return password == m.config.AdminPassword
}

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, sessionID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionDuration.Seconds()),
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// GetSessionID retrieves the session ID from the request cookie.
func GetSessionID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// CSRF token management

// GenerateCSRFToken creates a new CSRF token.
func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// SetCSRFCookie sets the CSRF cookie on the response.
func SetCSRFCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // JS needs to read this
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionDuration.Seconds()),
	})
}

// GetCSRFToken retrieves the CSRF token from the request.
func GetCSRFToken(r *http.Request) string {
	// Check header first (for AJAX)
	if token := r.Header.Get("X-CSRF-Token"); token != "" {
		return token
	}
	// Check form field
	if token := r.FormValue("csrf_token"); token != "" {
		return token
	}
	return ""
}

// GetCSRFCookie retrieves the CSRF cookie value.
func GetCSRFCookie(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// ValidateCSRF checks if the CSRF token matches.
func ValidateCSRF(r *http.Request) bool {
	cookie := GetCSRFCookie(r)
	token := GetCSRFToken(r)
	return cookie != "" && token != "" && cookie == token
}

// generateSessionID creates a random session ID.
func generateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// Context key for session
type contextKey string

const sessionContextKey contextKey = "session"

// WithSession adds a session to the context.
func WithSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, session)
}

// GetSession retrieves the session from the context.
func GetSession(ctx context.Context) *Session {
	session, ok := ctx.Value(sessionContextKey).(*Session)
	if !ok {
		return nil
	}
	return session
}

// RequireSession middleware ensures a valid session exists.
func (m *SessionManager) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := GetSessionID(r)
		if sessionID == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, err := m.ValidateSession(r.Context(), sessionID)
		if err != nil || session == nil {
			ClearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Refresh session
		m.RefreshSession(r.Context(), sessionID)

		// Add session to context
		ctx := WithSession(r.Context(), session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CSRFProtection middleware ensures CSRF token is valid for POST requests.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			if !ValidateCSRF(r) {
				http.Error(w, "CSRF token invalid", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
