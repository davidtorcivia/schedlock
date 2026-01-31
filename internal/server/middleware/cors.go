// Package middleware provides CORS and security headers middleware.
package middleware

import (
	"net/http"
	"strings"
)

// CORS returns middleware that handles Cross-Origin Resource Sharing.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		path := r.URL.Path

		// Allow CORS for callback endpoints (used by ntfy, pushover, etc.)
		// These endpoints are called by notification service clients
		if origin != "" && strings.HasPrefix(path, "/api/callback/") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
			w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours
		}

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			if strings.HasPrefix(path, "/api/callback/") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// For non-callback paths, return 204 but without CORS headers
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders returns middleware that adds security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Enable XSS filter (for older browsers)
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Content Security Policy
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
			"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://unpkg.com; "+ // Tailwind CDN + HTMX
			"style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; "+  // Tailwind CDN
			"img-src 'self' data:; "+
			"connect-src 'self'; "+
			"frame-ancestors 'none'")

		// Referrer Policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions Policy (formerly Feature Policy)
		w.Header().Set("Permissions-Policy",
			"geolocation=(), "+
			"microphone=(), "+
			"camera=(), "+
			"payment=(), "+
			"usb=()")

		next.ServeHTTP(w, r)
	})
}
