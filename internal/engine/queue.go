// Package engine provides the execution queue for processing approved requests.
package engine

import (
	"context"
	"sync"

	"github.com/dtorcivia/schedlock/internal/util"
)

// ExecutionQueue manages the queue of requests to be executed.
// Uses a single worker to serialize writes to Google Calendar and SQLite.
type ExecutionQueue struct {
	ch       chan string
	workers  int
	engine   *Engine
	wg       sync.WaitGroup
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExecutionQueue creates a new execution queue.
func NewExecutionQueue(workers int, engine *Engine) *ExecutionQueue {
	if workers < 1 {
		workers = 1
	}

	return &ExecutionQueue{
		ch:      make(chan string, 100),
		workers: workers,
		engine:  engine,
		stopCh:  make(chan struct{}),
	}
}

// Start begins processing the queue with worker goroutines.
func (q *ExecutionQueue) Start(ctx context.Context) {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx, i)
	}

	util.Info("Execution queue started", "workers", q.workers)
}

// Stop gracefully stops all workers.
func (q *ExecutionQueue) Stop() {
	q.stopOnce.Do(func() {
		close(q.stopCh)
		q.wg.Wait()
		util.Info("Execution queue stopped")
	})
}

// Enqueue adds a request ID to the execution queue.
func (q *ExecutionQueue) Enqueue(requestID string) {
	select {
	case q.ch <- requestID:
		util.Debug("Request enqueued", "request_id", requestID)
	default:
		// Queue is full, log warning
		util.Warn("Execution queue is full, request may be delayed", "request_id", requestID)
		// Try again with blocking
		q.ch <- requestID
	}
}

// worker processes requests from the queue.
func (q *ExecutionQueue) worker(ctx context.Context, id int) {
	defer q.wg.Done()

	util.Debug("Worker started", "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			util.Debug("Worker stopping due to context cancellation", "worker_id", id)
			return
		case <-q.stopCh:
			util.Debug("Worker stopping due to stop signal", "worker_id", id)
			return
		case requestID := <-q.ch:
			q.processRequest(ctx, requestID)
		}
	}
}

// processRequest executes a single request.
func (q *ExecutionQueue) processRequest(ctx context.Context, requestID string) {
	util.Debug("Processing request", "request_id", requestID)

	// Create a timeout context for execution
	execCtx, cancel := context.WithTimeout(ctx, q.engine.config.Server.WriteTimeout)
	defer cancel()

	if err := q.engine.ExecuteRequest(execCtx, requestID); err != nil {
		util.Error("Request execution failed", "request_id", requestID, "error", err)
	}
}

// Len returns the current queue length.
func (q *ExecutionQueue) Len() int {
	return len(q.ch)
}

// Pending returns the number of items waiting in the queue.
func (q *ExecutionQueue) Pending() int {
	return len(q.ch)
}
