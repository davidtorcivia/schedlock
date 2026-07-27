// Package middleware provides per-key rate limiting using token buckets.
package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
)

// bucketIdleTTL is how long an unused bucket is kept before eviction.
const bucketIdleTTL = time.Hour

// RateLimiter applies a per-API-key token bucket sized by the key's tier.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	limits  config.RateLimitsConfig
	now     func() time.Time // injectable for tests
}

type tokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a rate limiter with the given per-tier limits.
func NewRateLimiter(limits config.RateLimitsConfig) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		limits:  limits,
		now:     time.Now,
	}
}

// Allow consumes a token for keyID, reporting whether the request may proceed.
func (rl *RateLimiter) Allow(keyID, tier string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.buckets[keyID]
	if !exists {
		bucket = rl.newBucket(tier)
		rl.buckets[keyID] = bucket
	}

	now := rl.now()
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.lastRefill = now
	bucket.tokens = min(bucket.maxTokens, bucket.tokens+elapsed*bucket.refillRate)

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}
	return false
}

func (rl *RateLimiter) newBucket(tier string) *tokenBucket {
	var limit config.TierLimit
	switch tier {
	case "read":
		limit = rl.limits.Read
	case "admin":
		limit = rl.limits.Admin
	default:
		// Unknown tiers get the most restrictive configured limit.
		limit = rl.limits.Write
	}

	burst := float64(limit.Burst)
	if burst < 1 {
		burst = 1
	}

	return &tokenBucket{
		tokens:     burst,
		maxTokens:  burst,
		refillRate: float64(limit.RequestsPerMinute) / 60,
		lastRefill: rl.now(),
	}
}

// StartCleanup evicts buckets for keys that have gone quiet, so the map cannot
// grow without bound over the life of the process.
func (rl *RateLimiter) StartCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.evictIdle(bucketIdleTTL)
		}
	}
}

func (rl *RateLimiter) evictIdle(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := rl.now().Add(-maxAge)
	for keyID, bucket := range rl.buckets {
		if bucket.lastRefill.Before(cutoff) {
			delete(rl.buckets, keyID)
		}
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
