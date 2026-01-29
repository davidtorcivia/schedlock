// Package middleware provides HTTP middleware for the SchedLock server.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/response"
)

// ContextKey is a custom type for context keys.
type ContextKey string

const (
	// ContextKeyAPIKey is the context key for the authenticated API key.
	ContextKeyAPIKey ContextKey = "api_key"
	// ContextKeySession is the context key for the authenticated session.
	ContextKeySession ContextKey = "session"
)

// APIKeyFromContext extracts the API key from the request context.
func APIKeyFromContext(ctx context.Context) *apikeys.AuthenticatedKey {
	if key, ok := ctx.Value(ContextKeyAPIKey).(*apikeys.AuthenticatedKey); ok {
		return key
	}
	return nil
}

// SessionFromContext extracts the session from the request context.
func SessionFromContext(ctx context.Context) *database.Session {
	if session, ok := ctx.Value(ContextKeySession).(*database.Session); ok {
		return session
	}
	return nil
}

// GetAuthenticatedKey extracts the authenticated API key from an HTTP request.
func GetAuthenticatedKey(r *http.Request) *apikeys.AuthenticatedKey {
	return APIKeyFromContext(r.Context())
}

// APIKeyAuth returns middleware that validates API key authentication.
func APIKeyAuth(repo *apikeys.Repository, limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract API key from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				response.WriteInvalidAPIKey(w)
				return
			}

			// Expect "Bearer <key>" format
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				response.WriteInvalidAPIKey(w)
				return
			}

			apiKey := strings.TrimSpace(parts[1])
			if apiKey == "" {
				response.WriteInvalidAPIKey(w)
				return
			}

			// Validate API key
			authKey, err := repo.Authenticate(r.Context(), apiKey)
			if err != nil {
				response.WriteInvalidAPIKey(w)
				return
			}

			// Check rate limits
			if limiter != nil && !limiter.Allow(authKey.ID, authKey.Tier) {
				response.WriteRateLimited(w, 60) // Retry after 60 seconds
				return
			}

			// Update last used timestamp (async, don't block request)
			go repo.UpdateLastUsed(context.Background(), authKey.ID)

			// Add API key to context
			ctx := context.WithValue(r.Context(), ContextKeyAPIKey, authKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionAuth returns middleware that validates session-based authentication.
func SessionAuth(db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get session cookie
			cookie, err := r.Cookie("session_id")
			if err != nil || cookie.Value == "" {
				// Redirect to login for web pages
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			// Validate session
			var session database.Session
			err = db.QueryRow(`
				SELECT id, created_at, expires_at, last_activity, ip_address, user_agent, csrf_token
				FROM sessions
				WHERE id = ? AND expires_at > datetime('now')
			`, cookie.Value).Scan(
				&session.ID,
				&session.CreatedAt,
				&session.ExpiresAt,
				&session.LastActivity,
				&session.IPAddress,
				&session.UserAgent,
				&session.CSRFToken,
			)

			if err != nil {
				// Invalid or expired session, redirect to login
				http.SetCookie(w, &http.Cookie{
					Name:     "session_id",
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					Secure:   true,
					SameSite: http.SameSiteStrictMode,
				})
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			// Update last activity (sliding window)
			go func() {
				db.Exec(`
					UPDATE sessions
					SET last_activity = datetime('now')
					WHERE id = ?
				`, session.ID)
			}()

			// Add session to context
			ctx := context.WithValue(r.Context(), ContextKeySession, &session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireTier returns middleware that ensures the API key has the required tier.
func RequireTier(requiredTiers ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authKey := APIKeyFromContext(r.Context())
			if authKey == nil {
				response.WriteInvalidAPIKey(w)
				return
			}

			// Check if key's tier is in the required tiers
			allowed := false
			for _, tier := range requiredTiers {
				if authKey.Tier == tier {
					allowed = true
					break
				}
			}

			if !allowed {
				response.WriteInsufficientPermissions(w, authKey.Tier, r.URL.Path)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CSRFProtection returns middleware that validates CSRF tokens for POST requests.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check for state-changing methods
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		// Get session from context
		session := SessionFromContext(r.Context())
		if session == nil {
			response.WriteUnauthorized(w)
			return
		}

		// Get CSRF token from request (header or form)
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			csrfToken = r.FormValue("csrf_token")
		}

		if csrfToken == "" || csrfToken != session.CSRFToken {
			response.WriteError(w, http.StatusForbidden, "CSRF_VALIDATION_FAILED", "Invalid or missing CSRF token")
			return
		}

		next.ServeHTTP(w, r)
	})
}
