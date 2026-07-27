// Package middleware provides CORS and security response headers.
package middleware

import (
	"net/http"
	"strings"
)

// callbackPrefix is the only route family reachable cross-origin: notification
// clients (ntfy actions, chat clients) POST decision callbacks from arbitrary
// origins. Everything else is same-origin only.
const callbackPrefix = "/api/callback/"

// CORS permits cross-origin decision callbacks and rejects preflight for
// everything else.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isCallback := strings.HasPrefix(r.URL.Path, callbackPrefix)

		if isCallback {
			if origin := r.Header.Get("Origin"); origin != "" {
				// Credentials are never accepted cross-origin: the decision
				// token in the URL is the only authority, so echoing the
				// origin cannot be leveraged against a logged-in session.
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
		}

		if r.Method == http.MethodOptions {
			if isCallback {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets the response headers that constrain how a browser may
// treat SchedLock pages.
//
// The script policy carries real weight here: every page renders event titles,
// locations, and descriptions written by an AI agent. Scripts may load only
// from this origin, with no 'unsafe-inline', so injected markup that survived
// escaping still could not execute. This is why the UI carries no inline
// script or event-handler attributes.
//
// Styles still permit 'unsafe-inline' because the templates use style
// attributes for layout; that is a presentational risk, not an execution one.
// Google Fonts is the only off-origin source, and only for stylesheets and
// font files.
func SecurityHeaders(useTLS bool) func(http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data:",
		"connect-src 'self'",
		"form-action 'self'",
		"base-uri 'none'",
		"object-src 'none'",
		"frame-ancestors 'none'",
	}, "; ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			header.Set("X-Frame-Options", "DENY")
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Content-Security-Policy", csp)
			header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			header.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
			header.Set("Cross-Origin-Opener-Policy", "same-origin")

			if useTLS {
				header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}
