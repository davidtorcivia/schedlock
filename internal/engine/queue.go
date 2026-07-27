// Package engine provides the execution queue for approved requests.
package engine

import (
	"context"
	"sync"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// queueCapacity bounds the in-memory backlog. Approved requests are durable in
// the database, so a dropped queue entry delays execution until the request is
// re-queued rather than losing it.
const queueCapacity = 256

// executionTimeout caps a single calendar operation, including retries inside
// the Google client.
const executionTimeout = 2 * time.Minute

// ExecutionQueue serializes execution of approved requests.
type ExecutionQueue struct {
	ch       chan string
	workers  int
	engine   *Engine
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  chan struct{}
}

// NewExecutionQueue creates a queue with the given number of workers.
func NewExecutionQueue(workers int, engine *Engine) *ExecutionQueue {
	if workers < 1 {
		workers = 1
	}
	return &ExecutionQueue{
		ch:      make(chan string, queueCapacity),
		workers: workers,
		engine:  engine,
		stopped: make(chan struct{}),
	}
}

// Start launches the worker goroutines.
func (q *ExecutionQueue) Start(ctx context.Context) {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx, i)
	}
	util.Info("Execution queue started", "workers", q.workers)
}

// Stop signals the workers to finish the current item and drain the backlog,
// waiting until they do or the context expires.
//
// Shutting down mid-flight would leave a request marked "executing" with no
// worker ever returning to it, so the queue is drained deliberately rather than
// abandoned when the process is asked to stop.
func (q *ExecutionQueue) Stop(ctx context.Context) {
	q.stopOnce.Do(func() {
		close(q.stopped)

		done := make(chan struct{})
		go func() {
			q.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			util.Info("Execution queue stopped")
		case <-ctx.Done():
			util.Warn("Execution queue shutdown timed out with work in flight",
				"pending", len(q.ch))
		}
	})
}

// Enqueue submits a request for execution. It never blocks the caller: an
// overflowing queue is logged and the request stays approved for a later sweep.
func (q *ExecutionQueue) Enqueue(requestID string) {
	select {
	case q.ch <- requestID:
		util.Debug("Request enqueued", "request_id", requestID)
	default:
		util.Warn("Execution queue is full; request remains approved for retry",
			"request_id", requestID, "capacity", queueCapacity)
	}
}

// EnqueueAfter submits a request once delay has elapsed, unless the queue stops
// first.
func (q *ExecutionQueue) EnqueueAfter(requestID string, delay time.Duration) {
	if delay <= 0 {
		q.Enqueue(requestID)
		return
	}

	q.wg.Add(1)
	go func() {
		defer q.wg.Done()

		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			q.Enqueue(requestID)
		case <-q.stopped:
			util.Debug("Abandoning delayed retry during shutdown", "request_id", requestID)
		}
	}()
}

// Len reports the current backlog depth.
func (q *ExecutionQueue) Len() int { return len(q.ch) }

func (q *ExecutionQueue) worker(ctx context.Context, id int) {
	defer q.wg.Done()

	util.Debug("Execution worker started", "worker_id", id)

	for {
		select {
		case requestID := <-q.ch:
			q.processRequest(ctx, requestID)
		case <-q.stopped:
			q.drain(ctx, id)
			return
		case <-ctx.Done():
			util.Debug("Execution worker stopping", "worker_id", id)
			return
		}
	}
}

// drain executes whatever is already queued before the worker exits.
//
// Cancellation is detached: the caller's context may already be winding down,
// but a request that has been claimed for execution needs to reach a terminal
// state rather than being left marked "executing".
func (q *ExecutionQueue) drain(ctx context.Context, id int) {
	drainCtx := context.WithoutCancel(ctx)
	for {
		select {
		case requestID := <-q.ch:
			q.processRequest(drainCtx, requestID)
		default:
			util.Debug("Execution worker drained", "worker_id", id)
			return
		}
	}
}

func (q *ExecutionQueue) processRequest(ctx context.Context, requestID string) {
	execCtx, cancel := context.WithTimeout(ctx, executionTimeout)
	defer cancel()

	if err := q.engine.ExecuteRequest(execCtx, requestID); err != nil {
		util.Error("Request execution failed", "request_id", requestID, "error", err)
	}
}
