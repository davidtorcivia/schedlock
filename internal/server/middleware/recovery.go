// Package middleware provides panic recovery.
package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Recovery converts a panic in a handler into a 500 response and a logged
// stack trace, so one bad request cannot take the process down.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			// http.ErrAbortHandler is the documented way for a handler to drop
			// a connection; it is not a fault and must be re-raised.
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}

			util.Error("Panic recovered",
				"error", fmt.Sprintf("%v", rec),
				"path", RedactPath(r.URL.Path),
				"method", r.Method,
				"stack", string(debug.Stack()),
			)

			// A browser navigating the UI should not be handed a JSON body.
			if wantsHTML(r) {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			response.WriteError(w, http.StatusInternalServerError,
				response.CodeInternalError, "An unexpected error occurred")
		}()

		next.ServeHTTP(w, r)
	})
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
