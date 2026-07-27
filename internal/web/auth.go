package web

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

const (
	sessionCookieName = "schedlock_session"
	csrfCookieName    = "schedlock_csrf"

	// csrfCookieLifetime bounds a pre-session CSRF token, which only needs to
	// survive filling in the login form.
	csrfCookieLifetime = time.Hour
)

// Session is an authenticated web session.
type Session struct {
	ID        string
	UserID    string
	IPAddress string
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
	CSRFToken string
}

// SessionManager creates and validates web sessions.
type SessionManager struct {
	db     *database.DB
	config *config.AuthConfig
}

// NewSessionManager creates a session manager.
func NewSessionManager(db *database.DB, cfg *config.AuthConfig) *SessionManager {
	return &SessionManager{db: db, config: cfg}
}

// CreateSession issues a new session.
func (m *SessionManager) CreateSession(ctx context.Context, userID, ipAddress, userAgent string) (*Session, error) {
	sessionID, err := schedcrypto.GenerateSessionID()
	if err != nil {
		return nil, err
	}

	csrfToken, err := GenerateCSRFToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(m.SessionDuration())

	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO sessions (id, ip_address, user_agent, expires_at, csrf_token, last_activity)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, datetime('now'))
	`, sessionID, ipAddress, userAgent, util.SQLiteTimestamp(expiresAt), csrfToken); err != nil {
		return nil, err
	}

	return &Session{
		ID:        sessionID,
		UserID:    userID,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		CSRFToken: csrfToken,
	}, nil
}

// ValidateSession loads an unexpired session, or (nil, nil) if there is none.
func (m *SessionManager) ValidateSession(ctx context.Context, sessionID string) (*Session, error) {
	if sessionID == "" {
		return nil, nil
	}

	var (
		session   Session
		createdAt sql.NullString
		expiresAt sql.NullString
		ipAddress sql.NullString
		userAgent sql.NullString
	)

	err := m.db.QueryRowContext(ctx, `
		SELECT id, ip_address, user_agent, created_at, expires_at, csrf_token
		FROM sessions
		WHERE id = ? AND expires_at > datetime('now')
	`, sessionID).Scan(&session.ID, &ipAddress, &userAgent, &createdAt, &expiresAt, &session.CSRFToken)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	session.IPAddress = ipAddress.String
	session.UserAgent = userAgent.String
	if ts := database.NullTimeText(createdAt); ts.Valid {
		session.CreatedAt = ts.Time
	}
	if ts := database.NullTimeText(expiresAt); ts.Valid {
		session.ExpiresAt = ts.Time
	}
	session.UserID = "admin"

	return &session, nil
}

// DeleteSession removes a session.
func (m *SessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// RefreshSession extends a session's lifetime.
func (m *SessionManager) RefreshSession(ctx context.Context, sessionID string) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE sessions SET expires_at = ?, last_activity = datetime('now') WHERE id = ?
	`, util.SQLiteTimestamp(time.Now().Add(m.SessionDuration())), sessionID)
	return err
}

// VerifyPassword checks a submitted admin password.
func (m *SessionManager) VerifyPassword(password string) bool {
	if m.config.AdminPasswordHash != "" {
		ok, err := schedcrypto.VerifyPassword(password, m.config.AdminPasswordHash)
		if err != nil {
			util.Error("Stored admin password hash is unusable", "error", err)
			return false
		}
		return ok
	}

	if m.config.AdminPassword == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(m.config.AdminPassword)) == 1
}

// SessionDuration is how long a new session lasts.
func (m *SessionManager) SessionDuration() time.Duration {
	if m.config.SessionDuration <= 0 {
		return 24 * time.Hour
	}
	return m.config.SessionDuration
}

// RequireSession rejects unauthenticated requests, redirecting to the login
// page with the original destination preserved.
func (m *SessionManager) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := m.ValidateSession(r.Context(), sessionIDFrom(r))
		if err != nil {
			util.Error("Failed to validate session", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if session == nil {
			ClearSessionCookie(w)
			redirectToLogin(w, r)
			return
		}

		if m.config.SessionRefresh {
			if err := m.RefreshSession(r.Context(), session.ID); err != nil {
				util.Warn("Failed to refresh session", "error", err)
			}
		}

		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), session)))
	})
}

// redirectToLogin sends an unauthenticated visitor to the login page.
func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if r.Method == http.MethodGet {
		if next := SafeRedirect(r.URL.RequestURI(), ""); next != "" {
			target += "?redirect=" + url.QueryEscape(next)
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// CSRFProtection validates the CSRF token on every state-changing request.
//
// The submitted token is compared against the value stored with the session,
// not merely against a cookie. A cookie-only comparison can be defeated by
// anything able to write a cookie for this site, such as a compromised
// neighbouring subdomain; the session-bound token cannot.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		submitted := submittedCSRFToken(r)
		expected := ""
		if session := SessionFrom(r.Context()); session != nil {
			expected = session.CSRFToken
		} else {
			expected = csrfCookieValue(r)
		}

		if expected == "" || submitted == "" ||
			subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) != 1 {
			http.Error(w, "Invalid or missing CSRF token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GenerateCSRFToken creates a CSRF token.
func GenerateCSRFToken() (string, error) {
	return schedcrypto.GenerateCSRFToken()
}

// SetSessionCookie stores the session cookie.
func SetSessionCookie(w http.ResponseWriter, sessionID string, secure bool, duration time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		// Lax rather than Strict: the OAuth provider redirects back to this
		// site, and a Strict cookie would not be sent on that navigation.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(duration.Seconds()),
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SetCSRFCookie stores the CSRF cookie used before a session exists.
func SetCSRFCookie(w http.ResponseWriter, token string, secure bool, duration time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(duration.Seconds()),
	})
}

func sessionIDFrom(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func csrfCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func submittedCSRFToken(r *http.Request) string {
	if token := r.Header.Get("X-CSRF-Token"); token != "" {
		return token
	}
	return r.FormValue("csrf_token")
}

// contextKey is unexported so no other package can collide with it.
type contextKey string

const sessionContextKey contextKey = "session"

// WithSession attaches a session to a context.
func WithSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, session)
}

// SessionFrom retrieves the session from a context, or nil.
func SessionFrom(ctx context.Context) *Session {
	session, _ := ctx.Value(sessionContextKey).(*Session)
	return session
}
