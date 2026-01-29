// Package middleware provides request logging middleware.
package middleware

import (
	"net/http"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// responseWriter wraps http.ResponseWriter to capture status code and size.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// Logging returns middleware that logs HTTP requests.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		rw := newResponseWriter(w)

		// Process request
		next.ServeHTTP(rw, r)

		// Calculate duration
		duration := time.Since(start)

		// Get client IP (handle proxies)
		clientIP := r.Header.Get("X-Forwarded-For")
		if clientIP == "" {
			clientIP = r.Header.Get("X-Real-IP")
		}
		if clientIP == "" {
			clientIP = r.RemoteAddr
		}

		// Get API key prefix if authenticated
		var apiKeyPrefix string
		if authKey := APIKeyFromContext(r.Context()); authKey != nil {
			apiKeyPrefix = authKey.KeyPrefix
		}

		// Log the request
		logFields := map[string]interface{}{
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      rw.statusCode,
			"duration_ms": duration.Milliseconds(),
			"size":        rw.size,
			"client_ip":   clientIP,
			"user_agent":  r.UserAgent(),
		}

		if apiKeyPrefix != "" {
			logFields["api_key"] = apiKeyPrefix
		}

		if r.URL.RawQuery != "" {
			logFields["query"] = r.URL.RawQuery
		}

		// Log at appropriate level based on status code
		logger := util.GetDefaultLogger().WithFields(logFields)

		switch {
		case rw.statusCode >= 500:
			logger.Error("HTTP request")
		case rw.statusCode >= 400:
			logger.Warn("HTTP request")
		default:
			logger.Info("HTTP request")
		}
	})
}

// RequestID returns middleware that adds a unique request ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for existing request ID header
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			// Generate new request ID
			var err error
			requestID, err = util.GenerateRequestID()
			if err != nil {
				requestID = "unknown"
			}
		}

		// Set request ID in response header
		w.Header().Set("X-Request-ID", requestID)

		next.ServeHTTP(w, r)
	})
}

// Helper function to generate request IDs (moved here to avoid import cycle)
func generateRequestID() (string, error) {
	// Use crypto/tokens helper
	return util.GenerateRequestID()
}
