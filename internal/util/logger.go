// Package util provides structured logging built on log/slog.
package util

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// Logger is a thin wrapper over slog.Logger that keeps SchedLock's
// key/value call style while delegating formatting, levelling, and
// concurrency safety to the standard library.
type Logger struct {
	slog *slog.Logger
}

// NewLogger creates a logger writing to stdout at the given level, in either
// "json" or "text" format.
func NewLogger(level, format string) *Logger {
	return NewLoggerWithOutput(os.Stdout, level, format)
}

// NewLoggerWithOutput creates a logger writing to w. Used by tests.
func NewLoggerWithOutput(w io.Writer, level, format string) *Logger {
	opts := &slog.HandlerOptions{Level: ParseLogLevel(level)}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return &Logger{slog: slog.New(handler)}
}

// ParseLogLevel maps a configured level name onto an slog level, defaulting to
// info for unrecognised values.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithFields returns a logger that adds the given fields to every record.
func (l *Logger) WithFields(fields map[string]any) *Logger {
	if len(fields) == 0 {
		return l
	}
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	return &Logger{slog: l.slog.With(attrs...)}
}

// Debug logs at debug level. Arguments are alternating keys and values.
func (l *Logger) Debug(msg string, args ...any) { l.slog.Debug(msg, args...) }

// Info logs at info level.
func (l *Logger) Info(msg string, args ...any) { l.slog.Info(msg, args...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...any) { l.slog.Warn(msg, args...) }

// Error logs at error level.
func (l *Logger) Error(msg string, args ...any) { l.slog.Error(msg, args...) }

// defaultLogger is swapped at runtime when logging settings change, so it is
// held in an atomic pointer rather than a plain variable: requests read it
// concurrently with the settings handler replacing it.
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(NewLogger("info", "json"))
}

// SetDefaultLogger replaces the package-level logger.
func SetDefaultLogger(l *Logger) {
	if l != nil {
		defaultLogger.Store(l)
	}
}

// GetDefaultLogger returns the package-level logger.
func GetDefaultLogger() *Logger {
	return defaultLogger.Load()
}

// Debug logs to the default logger.
func Debug(msg string, args ...any) { GetDefaultLogger().Debug(msg, args...) }

// Info logs to the default logger.
func Info(msg string, args ...any) { GetDefaultLogger().Info(msg, args...) }

// Warn logs to the default logger.
func Warn(msg string, args ...any) { GetDefaultLogger().Warn(msg, args...) }

// Error logs to the default logger.
func Error(msg string, args ...any) { GetDefaultLogger().Error(msg, args...) }
