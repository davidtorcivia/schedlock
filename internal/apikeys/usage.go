package apikeys

import (
	"context"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// UsageRecorder batches "last used" bookkeeping for API keys.
//
// Recording usage inline would add a write to the critical path of every
// authenticated request, and spawning a goroutine per request (the previous
// approach) left an unbounded number of writers competing for SQLite's single
// writer under load. Instead, key IDs are collected and flushed periodically:
// last_used_at only needs to be approximately current.
type UsageRecorder struct {
	repo     *Repository
	pending  chan string
	interval time.Duration
}

// NewUsageRecorder creates a recorder flushing at the given interval.
func NewUsageRecorder(repo *Repository, interval time.Duration) *UsageRecorder {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &UsageRecorder{
		repo:     repo,
		pending:  make(chan string, 1024),
		interval: interval,
	}
}

// RecordUsage notes that a key was used. It never blocks: if the buffer is
// full, this observation is dropped rather than delaying the request.
func (u *UsageRecorder) RecordUsage(keyID string) {
	if keyID == "" {
		return
	}
	select {
	case u.pending <- keyID:
	default:
	}
}

// Run flushes batched usage until the context is cancelled, then writes
// whatever remains so a clean shutdown does not lose the last interval.
func (u *UsageRecorder) Run(ctx context.Context) {
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			u.flush(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			u.flush(ctx)
		}
	}
}

func (u *UsageRecorder) flush(ctx context.Context) {
	seen := make(map[string]struct{})
	ids := make([]string, 0, 16)

	for {
		select {
		case id := <-u.pending:
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
			continue
		default:
		}
		break
	}

	if len(ids) == 0 {
		return
	}

	if err := u.repo.UpdateLastUsed(ctx, ids...); err != nil {
		util.Warn("Failed to record API key usage", "error", err, "keys", len(ids))
	}
}
