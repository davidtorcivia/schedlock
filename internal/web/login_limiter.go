package web

import (
	"sync"
	"time"
)

type loginBucket struct {
	count int
	reset time.Time
}

// LoginLimiter provides a simple per-key fixed-window limiter.
type LoginLimiter struct {
	mu          sync.Mutex
	attempts    map[string]*loginBucket
	maxAttempts int
	window      time.Duration
}

// NewLoginLimiter creates a new limiter with the given limits.
func NewLoginLimiter(maxAttempts int, window time.Duration) *LoginLimiter {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &LoginLimiter{
		attempts:    make(map[string]*loginBucket),
		maxAttempts: maxAttempts,
		window:      window,
	}
}

// Allow records an attempt for the key and returns whether it is permitted.
func (l *LoginLimiter) Allow(key string) bool {
	if key == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	bucket, ok := l.attempts[key]
	if !ok || now.After(bucket.reset) {
		bucket = &loginBucket{count: 0, reset: now.Add(l.window)}
		l.attempts[key] = bucket
	}

	if bucket.count >= l.maxAttempts {
		return false
	}

	bucket.count++
	return true
}

// Reset clears attempts for a key (on successful login).
func (l *LoginLimiter) Reset(key string) {
	if key == "" {
		return
	}
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}
