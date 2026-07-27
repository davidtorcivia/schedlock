package web

import (
	"sync"
	"time"
)

// maxTrackedKeys bounds the limiter's memory.
//
// Attempts are keyed by client address, which an attacker behind a trusted
// proxy can vary freely through a forwarded header. Without a ceiling, that
// turns the limiter itself into a memory-exhaustion vector.
const maxTrackedKeys = 8192

// AttemptLimiter is a fixed-window limiter for authentication attempts.
type AttemptLimiter struct {
	mu          sync.Mutex
	attempts    map[string]*attemptWindow
	maxAttempts int
	window      time.Duration
	now         func() time.Time // injectable for tests
}

type attemptWindow struct {
	count int
	reset time.Time
}

// NewAttemptLimiter creates a limiter allowing maxAttempts per window.
func NewAttemptLimiter(maxAttempts int, window time.Duration) *AttemptLimiter {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &AttemptLimiter{
		attempts:    make(map[string]*attemptWindow),
		maxAttempts: maxAttempts,
		window:      window,
		now:         time.Now,
	}
}

// Allow records an attempt and reports whether it is permitted.
//
// An empty key means the caller could not be identified, in which case attempts
// are counted against a shared bucket rather than waved through.
func (l *AttemptLimiter) Allow(key string) bool {
	if key == "" {
		key = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	window, ok := l.attempts[key]
	if !ok || now.After(window.reset) {
		l.evictExpiredLocked(now)
		window = &attemptWindow{reset: now.Add(l.window)}
		l.attempts[key] = window
	}

	if window.count >= l.maxAttempts {
		return false
	}

	window.count++
	return true
}

// Reset clears the attempts recorded for a key, called after a success.
func (l *AttemptLimiter) Reset(key string) {
	if key == "" {
		return
	}
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// evictExpiredLocked drops finished windows, and if the map is still at its
// ceiling, clears it outright rather than growing without bound.
func (l *AttemptLimiter) evictExpiredLocked(now time.Time) {
	if len(l.attempts) < maxTrackedKeys {
		return
	}

	for key, window := range l.attempts {
		if now.After(window.reset) {
			delete(l.attempts, key)
		}
	}

	if len(l.attempts) >= maxTrackedKeys {
		// Every tracked window is still live: the limiter is under attack from
		// many sources. Starting over is safer than unbounded growth, and the
		// windows are short.
		l.attempts = make(map[string]*attemptWindow)
	}
}
