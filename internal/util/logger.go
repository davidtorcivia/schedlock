// Package util provides a structured logger for the application.
package util

import (
	"crypto/hmac"
	cryptoRandLib "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

func init() {
	cryptoRand = cryptoRandLib.Reader
}

// LogLevel represents logging severity levels.
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLogLevel converts a string to LogLevel.
func ParseLogLevel(s string) LogLevel {
	switch s {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger provides structured logging.
type Logger struct {
	mu     sync.Mutex
	output io.Writer
	level  LogLevel
	format string // "json" or "text"
	fields map[string]interface{}
}

// NewLogger creates a new logger.
func NewLogger(level, format string) *Logger {
	return &Logger{
		output: os.Stdout,
		level:  ParseLogLevel(level),
		format: format,
		fields: make(map[string]interface{}),
	}
}

// SetOutput sets the output writer.
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = w
}

// With returns a new logger with additional fields.
func (l *Logger) With(key string, value interface{}) *Logger {
	newFields := make(map[string]interface{}, len(l.fields)+1)
	for k, v := range l.fields {
		newFields[k] = v
	}
	newFields[key] = value

	return &Logger{
		output: l.output,
		level:  l.level,
		format: l.format,
		fields: newFields,
	}
}

// WithFields returns a new logger with multiple additional fields.
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	newFields := make(map[string]interface{}, len(l.fields)+len(fields))
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}

	return &Logger{
		output: l.output,
		level:  l.level,
		format: l.format,
		fields: newFields,
	}
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.log(LevelDebug, msg, args...)
}

// Info logs at info level.
func (l *Logger) Info(msg string, args ...interface{}) {
	l.log(LevelInfo, msg, args...)
}

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.log(LevelWarn, msg, args...)
}

// Error logs at error level.
func (l *Logger) Error(msg string, args ...interface{}) {
	l.log(LevelError, msg, args...)
}

func (l *Logger) log(level LogLevel, msg string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Build fields from varargs (key, value pairs)
	fields := make(map[string]interface{}, len(l.fields)+len(args)/2)
	for k, v := range l.fields {
		fields[k] = v
	}

	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			fields[key] = args[i+1]
		}
	}

	if l.format == "json" {
		l.logJSON(level, msg, fields)
	} else {
		l.logText(level, msg, fields)
	}
}

func (l *Logger) logJSON(level LogLevel, msg string, fields map[string]interface{}) {
	entry := map[string]interface{}{
		"time":  time.Now().UTC().Format(time.RFC3339),
		"level": level.String(),
		"msg":   msg,
	}

	for k, v := range fields {
		// Convert errors to strings to avoid JSON marshal issues
		if err, ok := v.(error); ok {
			entry[k] = err.Error()
		} else {
			entry[k] = v
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(l.output, `{"time":"%s","level":"error","msg":"failed to marshal log entry"}%s`,
			time.Now().UTC().Format(time.RFC3339), "\n")
		return
	}

	l.output.Write(append(data, '\n'))
}

func (l *Logger) logText(level LogLevel, msg string, fields map[string]interface{}) {
	timestamp := time.Now().UTC().Format("2006-01-02 15:04:05")

	var fieldsStr string
	for k, v := range fields {
		fieldsStr += fmt.Sprintf(" %s=%v", k, v)
	}

	fmt.Fprintf(l.output, "%s [%s] %s%s\n", timestamp, level.String(), msg, fieldsStr)
}

// Default logger instance
var defaultLogger = NewLogger("info", "json")

// SetDefaultLogger sets the default logger.
func SetDefaultLogger(l *Logger) {
	defaultLogger = l
}

// GetDefaultLogger returns the default logger.
func GetDefaultLogger() *Logger {
	return defaultLogger
}

// Package-level convenience functions

func Debug(msg string, args ...interface{}) {
	defaultLogger.Debug(msg, args...)
}

func Info(msg string, args ...interface{}) {
	defaultLogger.Info(msg, args...)
}

func Warn(msg string, args ...interface{}) {
	defaultLogger.Warn(msg, args...)
}

func Error(msg string, args ...interface{}) {
	defaultLogger.Error(msg, args...)
}

// ComputeHMAC computes HMAC-SHA256 signature of data.
func ComputeHMAC(data []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateRequestID generates a unique request ID.
func GenerateRequestID() (string, error) {
	// Generate a simple unique ID using timestamp and random bytes
	// For a real implementation, consider using a more robust ID generator like nanoid
	timestamp := time.Now().UnixNano()
	randomBytes := make([]byte, 8)
	if _, err := io.ReadFull(cryptoRand, randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("req_%x%x", timestamp, randomBytes), nil
}

// cryptoRand is the crypto/rand reader for secure random generation
var cryptoRand io.Reader
