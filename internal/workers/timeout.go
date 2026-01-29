// Package workers provides background worker goroutines.
package workers

import (
	"context"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/util"
)

// TimeoutWorker handles expiration of pending requests.
type TimeoutWorker struct {
	requestRepo *requests.Repository
	db          *database.DB
	interval    time.Duration
	webhookChan chan<- string // Channel to notify webhook client of expirations
}

// NewTimeoutWorker creates a new timeout worker.
func NewTimeoutWorker(requestRepo *requests.Repository, db *database.DB, interval time.Duration) *TimeoutWorker {
	return &TimeoutWorker{
		requestRepo: requestRepo,
		db:          db,
		interval:    interval,
	}
}

// SetWebhookChannel sets the channel for webhook notifications.
func (w *TimeoutWorker) SetWebhookChannel(ch chan<- string) {
	w.webhookChan = ch
}

// Start starts the timeout worker.
func (w *TimeoutWorker) Start(ctx context.Context) {
	util.Info("Starting timeout worker", "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start
	w.processExpired(ctx)

	for {
		select {
		case <-ctx.Done():
			util.Info("Timeout worker stopping")
			return
		case <-ticker.C:
			w.processExpired(ctx)
		}
	}
}

// processExpired finds and marks expired requests.
func (w *TimeoutWorker) processExpired(ctx context.Context) {
	// Find expired pending requests
	expired, err := w.requestRepo.GetExpired(ctx)
	if err != nil {
		util.Error("Failed to get expired requests", "error", err)
		return
	}

	if len(expired) == 0 {
		return
	}

	util.Info("Processing expired requests", "count", len(expired))

	for _, req := range expired {
		// Atomically update status to expired
		updated, err := w.requestRepo.UpdateStatusFrom(ctx, req.ID, database.StatusPendingApproval, database.StatusExpired)
		if err != nil {
			util.Error("Failed to expire request", "error", err, "request_id", req.ID)
			continue
		}

		if !updated {
			// Request was already decided by another process
			continue
		}

		// Log to audit
		w.logAudit(ctx, req.ID, req.APIKeyID)

		// Notify webhook client
		if w.webhookChan != nil {
			select {
			case w.webhookChan <- req.ID:
			default:
				util.Warn("Webhook channel full, dropping notification", "request_id", req.ID)
			}
		}

		util.Info("Request expired", "request_id", req.ID)
	}
}

// logAudit logs an expiration event to the audit log.
func (w *TimeoutWorker) logAudit(ctx context.Context, requestID, apiKeyID string) {
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_type, request_id, api_key_id, actor, details)
		VALUES (?, ?, ?, ?, NULL)
	`, database.AuditRequestExpired, requestID, apiKeyID, "timeout_worker")

	if err != nil {
		util.Error("Failed to log expiration audit", "error", err)
	}
}
