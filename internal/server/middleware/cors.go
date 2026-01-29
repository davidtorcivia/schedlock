// Package middleware provides CORS and security headers middleware.
package middleware

import (
	"net/http"
)

// CORS returns middleware that handles Cross-Origin Resource Sharing.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only allow same-origin requests by default
		// The proxy is primarily accessed by Moltbot (server-side) and the web UI (same origin)
		origin := r.Header.Get("Origin")

		// If no origin header, it's a same-origin request or non-browser client
		if origin != "" {
			// For API requests, we could allow specific origins
			// For now, we don't set CORS headers (same-origin only)
		}

		// Handle preflight requests
		if r.Method == http.MethodOptions {
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
			"script-src 'self' 'unsafe-inline'; "+ // Needed for HTMX inline handlers
			"style-src 'self' 'unsafe-inline'; "+  // Needed for Tailwind
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
