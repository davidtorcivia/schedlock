// Package middleware provides HTTP middleware for the SchedLock server.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/util"
)

// contextKey is unexported so no other package can collide with these keys.
type contextKey string

const contextKeyAPIKey contextKey = "api_key"

// WithAPIKey attaches an authenticated API key to a context.
func WithAPIKey(ctx context.Context, key *apikeys.AuthenticatedKey) context.Context {
	return context.WithValue(ctx, contextKeyAPIKey, key)
}

// APIKeyFromContext extracts the authenticated API key from a context.
func APIKeyFromContext(ctx context.Context) *apikeys.AuthenticatedKey {
	if key, ok := ctx.Value(contextKeyAPIKey).(*apikeys.AuthenticatedKey); ok {
		return key
	}
	return nil
}

// GetAuthenticatedKey extracts the authenticated API key from a request.
func GetAuthenticatedKey(r *http.Request) *apikeys.AuthenticatedKey {
	return APIKeyFromContext(r.Context())
}

// LastUsedRecorder records API key usage outside the request path.
type LastUsedRecorder interface {
	RecordUsage(keyID string)
}

// APIKeyAuth returns middleware that authenticates bearer API keys and applies
// per-key rate limits.
func APIKeyAuth(repo *apikeys.Repository, limiter *RateLimiter, usage LastUsedRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, ok := bearerToken(r)
			if !ok {
				response.WriteInvalidAPIKey(w)
				return
			}

			authKey, err := repo.Authenticate(r.Context(), apiKey)
			if err != nil {
				// The specific reason (unknown, revoked, expired) is deliberately
				// not echoed back; it is recorded server-side instead.
				util.Debug("API key authentication failed", "error", err)
				response.WriteInvalidAPIKey(w)
				return
			}

			if limiter != nil && !limiter.Allow(authKey.ID, authKey.Tier) {
				response.WriteRateLimited(w, 60)
				return
			}

			if usage != nil {
				usage.RecordUsage(authKey.ID)
			}

			next.ServeHTTP(w, r.WithContext(WithAPIKey(r.Context(), authKey)))
		})
	}
}

// bearerToken extracts a bearer credential from the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", false
	}

	scheme, credential, found := strings.Cut(authHeader, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	credential = strings.TrimSpace(credential)
	return credential, credential != ""
}
