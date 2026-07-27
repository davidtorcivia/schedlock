// Package workers provides the background goroutines that maintain request
// state and prune stored data.
package workers

import (
	"context"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/util"
)

// TimeoutWorker resolves requests whose approval window has elapsed.
type TimeoutWorker struct {
	requestRepo *requests.Repository
	engine      *engine.Engine
	auditLogger *engine.AuditLogger
	config      *config.ApprovalConfig
	interval    time.Duration
}

// NewTimeoutWorker creates a timeout worker polling at the given interval.
func NewTimeoutWorker(
	requestRepo *requests.Repository,
	eng *engine.Engine,
	auditLogger *engine.AuditLogger,
	cfg *config.ApprovalConfig,
	interval time.Duration,
) *TimeoutWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &TimeoutWorker{
		requestRepo: requestRepo,
		engine:      eng,
		auditLogger: auditLogger,
		config:      cfg,
		interval:    interval,
	}
}

// Start runs the worker until the context is cancelled.
func (w *TimeoutWorker) Start(ctx context.Context) {
	util.Info("Starting timeout worker", "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

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

// processExpired applies the configured default action to every request whose
// approval deadline has passed.
func (w *TimeoutWorker) processExpired(ctx context.Context) {
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
		if ctx.Err() != nil {
			return
		}
		w.resolve(ctx, req)
	}
}

func (w *TimeoutWorker) resolve(ctx context.Context, req database.Request) {
	// Anything other than an explicit "approve" default fails closed: an
	// unanswered request is denied rather than executed.
	if w.defaultAction() == "approve" && w.engine != nil {
		if err := w.engine.ProcessApproval(ctx, req.ID, "approve", "timeout"); err != nil {
			util.Error("Failed to auto-approve expired request", "error", err, "request_id", req.ID)
			return
		}
		util.Info("Request auto-approved on timeout", "request_id", req.ID)
		return
	}

	updated, err := w.requestRepo.UpdateStatus(ctx, req.ID, database.StatusExpired, "timeout")
	if err != nil {
		util.Error("Failed to expire request", "error", err, "request_id", req.ID)
		return
	}
	if !updated {
		// Someone decided the request between the query and this update.
		return
	}

	w.auditLogger.Log(ctx, engine.Entry{
		EventType: database.AuditRequestExpired,
		RequestID: req.ID,
		APIKeyID:  req.APIKeyID,
		Actor:     "timeout_worker",
	})

	if w.engine != nil {
		w.engine.NotifyWebhookStatus(ctx, req.ID, database.StatusExpired)
	}

	util.Info("Request expired", "request_id", req.ID)
}

func (w *TimeoutWorker) defaultAction() string {
	if w.config == nil || w.config.DefaultAction == "" {
		return "deny"
	}
	return w.config.DefaultAction
}
