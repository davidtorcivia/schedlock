// Package middleware provides panic recovery middleware.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/dtorcivia/schedlock/internal/response"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Recovery returns middleware that recovers from panics.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic with stack trace
				stack := debug.Stack()
				util.Error("Panic recovered",
					"error", fmt.Sprintf("%v", err),
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(stack),
				)

				// Return 500 error (don't expose internal details)
				response.WriteInternalError(w, "An unexpected error occurred")
			}
		}()

		next.ServeHTTP(w, r)
	})
}
