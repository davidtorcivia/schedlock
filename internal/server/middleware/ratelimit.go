// Package middleware provides rate limiting using the token bucket algorithm.
package middleware

import (
	"sync"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
)

// RateLimiter implements per-tier rate limiting using token buckets.
type RateLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*tokenBucket
	limits  config.RateLimitsConfig
}

// tokenBucket implements the token bucket algorithm.
type tokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter with the given configuration.
func NewRateLimiter(limits config.RateLimitsConfig) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		limits:  limits,
	}
}

// Allow checks if a request should be allowed based on the rate limit.
// Returns true if allowed, false if rate limited.
func (rl *RateLimiter) Allow(keyID, tier string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Get or create bucket for this key
	bucket, exists := rl.buckets[keyID]
	if !exists {
		bucket = rl.createBucket(tier)
		rl.buckets[keyID] = bucket
	}

	// Refill tokens based on elapsed time
	bucket.refill()

	// Check if we have tokens available
	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0
		return true
	}

	return false
}

// createBucket creates a new token bucket for the given tier.
func (rl *RateLimiter) createBucket(tier string) *tokenBucket {
	var limit config.TierLimit

	switch tier {
	case "read":
		limit = rl.limits.Read
	case "write":
		limit = rl.limits.Write
	case "admin":
		limit = rl.limits.Admin
	default:
		// Default to most restrictive
		limit = rl.limits.Write
	}

	return &tokenBucket{
		tokens:     float64(limit.Burst), // Start with full burst capacity
		maxTokens:  float64(limit.Burst),
		refillRate: float64(limit.RequestsPerMinute) / 60.0, // Convert to per-second
		lastRefill: time.Now(),
	}
}

// refill adds tokens based on elapsed time.
func (tb *tokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	// Add tokens based on time elapsed
	tb.tokens += elapsed * tb.refillRate

	// Cap at max tokens
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
}

// GetRemainingTokens returns the number of remaining tokens for a key.
func (rl *RateLimiter) GetRemainingTokens(keyID string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	bucket, exists := rl.buckets[keyID]
	if !exists {
		return 0
	}

	return int(bucket.tokens)
}

// Reset removes all rate limit state (useful for testing).
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets = make(map[string]*tokenBucket)
}

// Cleanup removes stale buckets that haven't been used recently.
func (rl *RateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for keyID, bucket := range rl.buckets {
		if bucket.lastRefill.Before(cutoff) {
			delete(rl.buckets, keyID)
		}
	}
}
