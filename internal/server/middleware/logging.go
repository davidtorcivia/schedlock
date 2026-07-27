// Package middleware provides request logging.
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// responseRecorder captures the status code and byte count of a response.
type responseRecorder struct {
	http.ResponseWriter
	statusCode  int
	size        int
	wroteHeader bool
}

func (rw *responseRecorder) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.statusCode = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// Flush lets streaming handlers through the wrapper.
func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logging returns middleware that logs one structured record per request.
func Logging(clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(rec, r)

			fields := map[string]any{
				"method":      r.Method,
				"path":        RedactPath(r.URL.Path),
				"status":      rec.statusCode,
				"duration_ms": time.Since(start).Milliseconds(),
				"size":        rec.size,
				"client_ip":   clientIP(r),
				"user_agent":  r.UserAgent(),
			}

			if authKey := APIKeyFromContext(r.Context()); authKey != nil {
				fields["api_key"] = authKey.KeyPrefix
			}
			if r.URL.RawQuery != "" {
				fields["query"] = redactQuery(r.URL.RawQuery)
			}

			logger := util.GetDefaultLogger().WithFields(fields)
			switch {
			case rec.statusCode >= 500:
				logger.Error("HTTP request")
			case rec.statusCode >= 400:
				logger.Warn("HTTP request")
			default:
				logger.Info("HTTP request")
			}
		})
	}
}

// redactedTokenPrefixes are URL path segments after which the next segment is a
// single-use approval credential.
var redactedTokenPrefixes = []string{
	"/api/callback/approve/",
	"/api/callback/deny/",
	"/api/callback/suggest/",
	"/approve/",
}

// RedactPath removes decision tokens from a URL path.
//
// Approval links carry a single-use credential in the path. Logging them
// verbatim would leave working approval tokens sitting in log aggregators and
// proxy logs for anyone with read access to replay.
func RedactPath(path string) string {
	for _, prefix := range redactedTokenPrefixes {
		if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
			return prefix + "[redacted]"
		}
	}
	return path
}

// sensitiveQueryParams are query parameter names whose values must never reach
// the logs: credentials, one-time codes, and free text supplied by the operator.
var sensitiveQueryParams = map[string]bool{
	"token":      true,
	"code":       true,
	"secret":     true,
	"key":        true,
	"created":    true,
	"pin":        true,
	"password":   true,
	"state":      true,
	"suggestion": true,
}

// redactQuery removes the values of sensitive query parameters.
func redactQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		name, _, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		if sensitiveQueryParams[strings.ToLower(name)] {
			parts[i] = name + "=[redacted]"
		}
	}
	return strings.Join(parts, "&")
}
