// Package engine provides the core request lifecycle: submission, approval,
// and execution against Google Calendar.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Errors surfaced to API callers.
var (
	ErrRequestNotFound = errors.New("request not found")
	ErrInvalidAction   = errors.New("invalid action")
)

// ErrAlreadyDecided reports an attempt to decide a request a second time.
type ErrAlreadyDecided struct {
	Status string
}

func (e *ErrAlreadyDecided) Error() string {
	return fmt.Sprintf("request has already been %s", e.Status)
}

// CalendarClient is the calendar behaviour the engine depends on.
type CalendarClient interface {
	CreateEvent(ctx context.Context, intent *google.EventIntent) (*google.Event, error)
	UpdateEvent(ctx context.Context, intent *google.EventUpdateIntent) (*google.Event, error)
	DeleteEvent(ctx context.Context, intent *google.EventDeleteIntent) error
}

// NotificationManager sends notifications to configured providers.
type NotificationManager interface {
	SendApprovalRequest(ctx context.Context, req *notifications.ApprovalNotification) error
	SendResult(ctx context.Context, result *notifications.ResultNotification)
}

// WebhookClient delivers request status updates to the calling system.
type WebhookClient interface {
	Deliver(ctx context.Context, event WebhookEvent) error
}

// WebhookEvent is one status update for the calling system.
type WebhookEvent struct {
	RequestID  string
	Status     string
	Message    string
	Suggestion string
	Result     json.RawMessage
}

// Engine orchestrates request processing, approvals, and execution.
type Engine struct {
	config         *config.Config
	requestRepo    *requests.Repository
	calendarClient CalendarClient
	notifier       NotificationManager
	webhookClient  WebhookClient
	executionQueue *ExecutionQueue
	auditLogger    *AuditLogger
	tokenRepo      *tokens.Repository
}

// NewEngine creates an engine with a single-worker execution queue, which
// serializes writes to Google Calendar and to SQLite.
func NewEngine(
	cfg *config.Config,
	requestRepo *requests.Repository,
	calendarClient CalendarClient,
	auditLogger *AuditLogger,
	tokenRepo *tokens.Repository,
) *Engine {
	e := &Engine{
		config:         cfg,
		requestRepo:    requestRepo,
		calendarClient: calendarClient,
		auditLogger:    auditLogger,
		tokenRepo:      tokenRepo,
	}
	e.executionQueue = NewExecutionQueue(1, e)
	return e
}

// SetNotifier sets the notification manager.
func (e *Engine) SetNotifier(n NotificationManager) { e.notifier = n }

// SetWebhookClient sets the outbound status webhook client.
func (e *Engine) SetWebhookClient(c WebhookClient) { e.webhookClient = c }

// Start launches the execution queue workers.
func (e *Engine) Start(ctx context.Context) { e.executionQueue.Start(ctx) }

// Stop drains in-flight executions and stops the queue.
func (e *Engine) Stop(ctx context.Context) { e.executionQueue.Stop(ctx) }

// SubmitRequest records a new request and either notifies approvers or, when
// policy permits, executes it immediately.
func (e *Engine) SubmitRequest(
	ctx context.Context,
	authKey *apikeys.AuthenticatedKey,
	operation string,
	payload json.RawMessage,
	idempotencyKey string,
	approvalRequired bool,
	decidedBy string,
) (*database.Request, error) {
	if idempotencyKey != "" {
		existing, err := e.requestRepo.FindByIdempotencyKey(ctx, authKey.ID, idempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("idempotency check failed: %w", err)
		}
		if existing != nil {
			util.Info("Returning existing request for idempotency key",
				"request_id", existing.ID, "idempotency_key", idempotencyKey)
			return existing, nil
		}
	}

	expiresAt := time.Now().Add(e.approvalTimeout())

	req, err := e.requestRepo.Create(ctx, &requests.CreateRequest{
		APIKeyID:  authKey.ID,
		Operation: operation,
		Payload:   payload,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Two concurrent submissions carrying the same idempotency key both get
	// past the lookup above. The loser of the insert adopts the winner's
	// request and discards its own, so the caller never sees two request IDs
	// for one key and no orphan request is left pending approval.
	if idempotencyKey != "" {
		claimed, err := e.requestRepo.ClaimIdempotencyKey(ctx, authKey.ID, idempotencyKey, req.ID)
		if err != nil {
			util.Warn("Failed to store idempotency key", "error", err, "request_id", req.ID)
		} else if !claimed {
			if err := e.requestRepo.Delete(ctx, req.ID); err != nil {
				util.Warn("Failed to discard duplicate request", "error", err, "request_id", req.ID)
			}
			winner, err := e.requestRepo.FindByIdempotencyKey(ctx, authKey.ID, idempotencyKey)
			if err != nil || winner == nil {
				return nil, fmt.Errorf("idempotency conflict for key %q", idempotencyKey)
			}
			return winner, nil
		}
	}

	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestCreated,
		RequestID: req.ID,
		APIKeyID:  authKey.ID,
		Actor:     "api",
		Details:   map[string]any{"operation": operation},
	})

	util.Info("Request submitted",
		"request_id", req.ID,
		"operation", operation,
		"approval_required", approvalRequired,
		"expires_at", expiresAt,
	)

	if !approvalRequired {
		if decidedBy == "" {
			decidedBy = "policy"
		}
		if err := e.ProcessApproval(ctx, req.ID, "approve", decidedBy); err != nil {
			return nil, err
		}
		if updated, err := e.requestRepo.GetByID(ctx, req.ID); err == nil && updated != nil {
			req = updated
		}
		return req, nil
	}

	// Notifications reach out over the network; they must not hold up the
	// caller's response, and they must survive the request context ending.
	notifyCtx := context.WithoutCancel(ctx)
	go e.sendApprovalNotifications(notifyCtx, req)

	return req, nil
}

// ProcessApproval records an approve or deny decision and, on approval, queues
// the request for execution.
func (e *Engine) ProcessApproval(ctx context.Context, requestID, action, decidedBy string) error {
	var newStatus string
	switch action {
	case "approve":
		newStatus = database.StatusApproved
	case "deny":
		newStatus = database.StatusDenied
	default:
		return fmt.Errorf("%w: %s", ErrInvalidAction, action)
	}

	updated, err := e.requestRepo.UpdateStatus(ctx, requestID, newStatus, decidedBy)
	if err != nil {
		return err
	}
	if !updated {
		req, err := e.requestRepo.GetByID(ctx, requestID)
		if err != nil {
			return err
		}
		if req == nil {
			return ErrRequestNotFound
		}
		return &ErrAlreadyDecided{Status: req.Status}
	}

	auditEvent := database.AuditRequestApproved
	if action == "deny" {
		auditEvent = database.AuditRequestDenied
	}
	e.auditLogger.Log(ctx, Entry{
		EventType: auditEvent,
		RequestID: requestID,
		Actor:     decidedBy,
	})

	if action == "approve" {
		e.executionQueue.Enqueue(requestID)
	}

	e.notifyWebhookAsync(ctx, requestID, newStatus, "")

	util.Info("Request decision processed",
		"request_id", requestID, "action", action, "decided_by", decidedBy)

	return nil
}

// ProcessSuggestion records requested changes against a pending request.
func (e *Engine) ProcessSuggestion(ctx context.Context, requestID, suggestion, suggestedBy string) error {
	suggestion = util.SanitizeText(suggestion)
	if suggestion == "" {
		return errors.New("suggestion text is required")
	}
	if err := util.ValidateLength("suggestion", suggestion, util.MaxDescriptionLength); err != nil {
		return err
	}

	if err := e.requestRepo.SetSuggestion(ctx, requestID, suggestion, suggestedBy); err != nil {
		return err
	}

	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestChanged,
		RequestID: requestID,
		Actor:     suggestedBy,
		Details:   map[string]any{"suggestion": suggestion},
	})

	e.notifyWebhookAsync(ctx, requestID, database.StatusChangeRequested, suggestion)

	util.Info("Suggestion recorded", "request_id", requestID, "suggested_by", suggestedBy)
	return nil
}

// GetRequest retrieves a request by ID.
func (e *Engine) GetRequest(ctx context.Context, requestID string) (*database.Request, error) {
	return e.requestRepo.GetByID(ctx, requestID)
}

// CancelRequest withdraws a pending request on behalf of its owning key.
func (e *Engine) CancelRequest(ctx context.Context, requestID, apiKeyID string) error {
	if err := e.requestRepo.Cancel(ctx, requestID, apiKeyID); err != nil {
		return err
	}

	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestCancelled,
		RequestID: requestID,
		APIKeyID:  apiKeyID,
		Actor:     "api",
	})
	return nil
}

// NotifyWebhookStatus delivers a status update for a request, used by the
// timeout worker after expiring a request.
func (e *Engine) NotifyWebhookStatus(ctx context.Context, requestID, status string) {
	e.notifyWebhook(ctx, requestID, status, "")
}

// ExecuteRequest performs the calendar operation behind an approved request.
func (e *Engine) ExecuteRequest(ctx context.Context, requestID string) error {
	req, err := e.requestRepo.GetByID(ctx, requestID)
	if err != nil {
		return err
	}
	if req == nil {
		return ErrRequestNotFound
	}

	// Claiming the request is what prevents a duplicate queue entry, a retry,
	// and a restart from all executing the same calendar write.
	claimed, err := e.requestRepo.SetExecuting(ctx, requestID)
	if err != nil {
		return err
	}
	if !claimed {
		util.Debug("Skipping request that is not awaiting execution",
			"request_id", requestID, "status", req.Status)
		return nil
	}

	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestExecuting,
		RequestID: requestID,
		APIKeyID:  req.APIKeyID,
		Actor:     "engine",
	})

	result, execErr := e.execute(ctx, req)
	if execErr != nil {
		return e.handleExecutionFailure(ctx, req, execErr)
	}

	var resultJSON json.RawMessage
	if result != nil {
		if resultJSON, err = json.Marshal(result); err != nil {
			util.Warn("Failed to encode execution result", "error", err, "request_id", requestID)
		}
	}
	if err := e.requestRepo.SetResult(ctx, requestID, resultJSON); err != nil {
		util.Error("Failed to store execution result", "error", err, "request_id", requestID)
	}

	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestCompleted,
		RequestID: requestID,
		APIKeyID:  req.APIKeyID,
		Actor:     "engine",
	})
	e.notifyWebhookAsync(ctx, requestID, database.StatusCompleted, "")
	e.notifyOutcome(ctx, req, database.StatusCompleted, "", eventLink(result))

	util.Info("Request executed successfully", "request_id", requestID)
	return nil
}

// notifyOutcome tells the approver what became of the request they decided.
//
// Approval is only half of the interaction: a request that is approved and then
// fails against Google would otherwise leave the operator believing the change
// was made.
func (e *Engine) notifyOutcome(ctx context.Context, req *database.Request, status, errText, eventURL string) {
	if e.notifier == nil {
		return
	}

	// The in-memory request predates the status write, so the failure reason is
	// taken from the error that was just recorded rather than re-read from it.
	message := statusMessage(req, status)
	if status == database.StatusFailed {
		message = "Your calendar request could not be completed."
		if errText != "" {
			message = fmt.Sprintf("Your calendar request could not be completed: %s",
				util.TruncateString(errText, 300))
		}
	}

	details := DescribeRequest(req)
	if details != nil && details.Title != "" {
		message = fmt.Sprintf("%s (%s)", message, details.Title)
	}

	notification := &notifications.ResultNotification{
		RequestID: req.ID,
		Operation: req.Operation,
		Status:    status,
		Message:   message,
		EventURL:  eventURL,
		Error:     errText,
	}

	sendCtx := context.WithoutCancel(ctx)
	go e.notifier.SendResult(sendCtx, notification)
}

// eventLink extracts the Google Calendar link from an execution result.
func eventLink(result any) string {
	if event, ok := result.(*google.Event); ok && event != nil {
		return event.HTMLLink
	}
	return ""
}

// execute dispatches to the calendar operation named by the request.
func (e *Engine) execute(ctx context.Context, req *database.Request) (any, error) {
	switch req.Operation {
	case database.OperationCreateEvent:
		var intent google.EventIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
		return e.calendarClient.CreateEvent(ctx, &intent)

	case database.OperationUpdateEvent:
		var intent google.EventUpdateIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
		return e.calendarClient.UpdateEvent(ctx, &intent)

	case database.OperationDeleteEvent:
		var intent google.EventDeleteIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
		return nil, e.calendarClient.DeleteEvent(ctx, &intent)

	default:
		return nil, fmt.Errorf("unknown operation: %s", req.Operation)
	}
}

// handleExecutionFailure either schedules a retry or records terminal failure.
func (e *Engine) handleExecutionFailure(ctx context.Context, req *database.Request, execErr error) error {
	if e.isRetryable(execErr) && req.RetryCount < e.config.Retry.MaxAttempts {
		if err := e.requestRepo.ScheduleRetry(ctx, req.ID); err != nil {
			util.Error("Failed to schedule retry", "error", err, "request_id", req.ID)
		} else {
			backoff := e.backoffFor(req.RetryCount)
			util.Warn("Request execution failed, retrying",
				"request_id", req.ID, "error", execErr,
				"attempt", req.RetryCount+1, "backoff", backoff)
			e.executionQueue.EnqueueAfter(req.ID, backoff)
			return nil
		}
	}

	if err := e.requestRepo.SetError(ctx, req.ID, execErr.Error()); err != nil {
		util.Error("Failed to record execution error", "error", err, "request_id", req.ID)
	}
	e.auditLogger.Log(ctx, Entry{
		EventType: database.AuditRequestFailed,
		RequestID: req.ID,
		APIKeyID:  req.APIKeyID,
		Actor:     "engine",
		Details:   map[string]any{"error": execErr.Error()},
	})
	e.notifyWebhookAsync(ctx, req.ID, database.StatusFailed, "")
	e.notifyOutcome(ctx, req, database.StatusFailed, execErr.Error(), "")

	return execErr
}

// isRetryable reports whether a failure is worth another attempt.
func (e *Engine) isRetryable(err error) bool {
	if !e.config.Retry.Enabled {
		return false
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		for _, code := range e.config.Retry.RetryableStatusCodes {
			if apiErr.Code == code {
				return true
			}
		}
		// A definitive answer from Google (404, 403, 400) will not change.
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// A cancelled context means shutdown, not a transient upstream fault; the
	// request stays approved and is picked up after restart.
	if errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

func (e *Engine) backoffFor(retryCount int) time.Duration {
	backoffs := e.config.Retry.BackoffSeconds
	if len(backoffs) == 0 {
		return 5 * time.Second
	}
	if retryCount >= len(backoffs) {
		retryCount = len(backoffs) - 1
	}
	if retryCount < 0 {
		retryCount = 0
	}
	return time.Duration(backoffs[retryCount]) * time.Second
}

func (e *Engine) approvalTimeout() time.Duration {
	minutes := e.config.Approval.TimeoutMinutes
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

// sendApprovalNotifications mints a decision token and fans the request out to
// every configured notification provider.
func (e *Engine) sendApprovalNotifications(ctx context.Context, req *database.Request) {
	if e.notifier == nil {
		return
	}

	var decisionToken string
	if e.tokenRepo != nil {
		token, err := e.tokenRepo.Create(ctx, req.ID, req.ExpiresAt)
		if err != nil {
			util.Error("Failed to create decision token", "error", err, "request_id", req.ID)
		} else {
			decisionToken = token
		}
	}

	details := DescribeRequest(req)
	notification := &notifications.ApprovalNotification{
		RequestID:     req.ID,
		Operation:     req.Operation,
		Summary:       SummarizeOperation(req.Operation, details),
		Details:       details,
		ExpiresAt:     req.ExpiresAt,
		ExpiresIn:     util.GetDefaultFormatter().FormatExpiresIn(req.ExpiresAt),
		DecisionToken: decisionToken,
	}

	if err := e.notifier.SendApprovalRequest(ctx, notification); err != nil {
		util.Error("Failed to send approval notifications", "error", err, "request_id", req.ID)
	}
}

// notifyWebhookAsync delivers a status update without blocking the caller.
func (e *Engine) notifyWebhookAsync(ctx context.Context, requestID, status, suggestion string) {
	if e.webhookClient == nil || !e.shouldNotify(status) {
		return
	}
	deliverCtx := context.WithoutCancel(ctx)
	go e.notifyWebhook(deliverCtx, requestID, status, suggestion)
}

func (e *Engine) notifyWebhook(ctx context.Context, requestID, status, suggestion string) {
	if e.webhookClient == nil || !e.shouldNotify(status) {
		return
	}

	req, err := e.requestRepo.GetByID(ctx, requestID)
	if err != nil || req == nil {
		util.Warn("Skipping webhook for unreadable request", "request_id", requestID, "error", err)
		return
	}

	event := WebhookEvent{
		RequestID:  requestID,
		Status:     status,
		Suggestion: suggestion,
		Result:     req.Result,
	}
	if suggestion != "" {
		event.Message = suggestionMessage(req, suggestion)
	} else {
		event.Message = statusMessage(req, status)
	}

	if err := e.webhookClient.Deliver(ctx, event); err != nil {
		util.Error("Failed to deliver webhook", "error", err, "request_id", requestID)
		return
	}

	if err := e.requestRepo.SetWebhookNotified(ctx, requestID); err != nil {
		util.Warn("Failed to record webhook delivery", "error", err, "request_id", requestID)
	}
}

func (e *Engine) shouldNotify(status string) bool {
	notifyOn := e.config.Moltbot.Webhook.NotifyOn
	if len(notifyOn) == 0 {
		return true
	}
	for _, allowed := range notifyOn {
		if allowed == status {
			return true
		}
	}
	return false
}

func statusMessage(req *database.Request, status string) string {
	switch status {
	case database.StatusApproved:
		return "Your calendar request has been approved and is being executed."
	case database.StatusDenied:
		return "Your calendar request was denied."
	case database.StatusCompleted:
		return "Your calendar request was completed successfully."
	case database.StatusFailed:
		return fmt.Sprintf("Your calendar request failed: %s", req.Error.String)
	case database.StatusExpired:
		return "Your calendar request expired without a response."
	case database.StatusCancelled:
		return "Your calendar request was cancelled."
	default:
		return fmt.Sprintf("Calendar request status: %s", status)
	}
}

func suggestionMessage(req *database.Request, suggestion string) string {
	return fmt.Sprintf(`Calendar request needs changes.

Operation: %s
Suggestion: %q

Please modify the request based on this feedback and resubmit.`, req.Operation, suggestion)
}
