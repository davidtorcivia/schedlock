// Package engine provides the core business logic for request processing.
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

// Engine orchestrates request processing, approvals, and execution.
type Engine struct {
	config         *config.Config
	requestRepo    *requests.Repository
	calendarClient *google.CalendarClient
	notifier       NotificationManager
	webhookClient  WebhookClient
	executionQueue *ExecutionQueue
	auditLogger    *AuditLogger
	tokenRepo      *tokens.Repository
}

// NotificationManager interface for sending approval notifications.
type NotificationManager interface {
	SendApprovalRequest(ctx context.Context, req *notifications.ApprovalNotification) error
}

// WebhookClient interface for sending Moltbot webhooks.
type WebhookClient interface {
	Deliver(ctx context.Context, event WebhookEvent) error
}

// WebhookEvent contains data for Moltbot webhook.
type WebhookEvent struct {
	RequestID  string
	Status     string
	Message    string
	Suggestion string
	Result     json.RawMessage
}

// NewEngine creates a new engine instance.
func NewEngine(
	cfg *config.Config,
	requestRepo *requests.Repository,
	calendarClient *google.CalendarClient,
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

	// Create execution queue with single worker
	e.executionQueue = NewExecutionQueue(1, e)

	return e
}

// SetNotifier sets the notification manager.
func (e *Engine) SetNotifier(n NotificationManager) {
	e.notifier = n
}

// SetWebhookClient sets the Moltbot webhook client.
func (e *Engine) SetWebhookClient(c WebhookClient) {
	e.webhookClient = c
}

// Start starts the execution queue workers.
func (e *Engine) Start(ctx context.Context) {
	e.executionQueue.Start(ctx)
}

// Stop gracefully stops the execution queue.
func (e *Engine) Stop() {
	e.executionQueue.Stop()
}

// QueueExecution enqueues a request for execution.
func (e *Engine) QueueExecution(requestID string) {
	e.executionQueue.Enqueue(requestID)
}

// NotifyWebhookStatus sends a webhook status update.
func (e *Engine) NotifyWebhookStatus(ctx context.Context, requestID, status string) {
	e.notifyWebhook(ctx, requestID, status)
}

// SubmitRequest creates a new request and sends notifications.
func (e *Engine) SubmitRequest(
	ctx context.Context,
	authKey *apikeys.AuthenticatedKey,
	operation string,
	payload json.RawMessage,
	idempotencyKey string,
	approvalRequired bool,
	decidedBy string,
) (*database.Request, error) {
	// Check idempotency key first
	if idempotencyKey != "" {
		existing, err := e.requestRepo.FindByIdempotencyKey(ctx, authKey.ID, idempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("idempotency check failed: %w", err)
		}
		if existing != nil {
			util.Info("Returning existing request for idempotency key",
				"request_id", existing.ID,
				"idempotency_key", idempotencyKey,
			)
			return existing, nil
		}
	}

	// Calculate expiry time
	expiresAt := time.Now().Add(time.Duration(e.config.Approval.TimeoutMinutes) * time.Minute)

	// Create the request
	req, err := e.requestRepo.Create(ctx, &requests.CreateRequest{
		APIKeyID:  authKey.ID,
		Operation: operation,
		Payload:   payload,
		ExpiresAt: expiresAt,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Store idempotency key if provided
	if idempotencyKey != "" {
		if err := e.requestRepo.StoreIdempotencyKey(ctx, authKey.ID, idempotencyKey, req.ID); err != nil {
			util.Warn("Failed to store idempotency key", "error", err)
		}
	}

	// Log to audit
	e.auditLogger.Log(ctx, database.AuditRequestCreated, req.ID, authKey.ID, "api", map[string]interface{}{
		"operation": operation,
	})

	if approvalRequired {
		// Send approval notifications (async)
		go e.sendApprovalNotifications(context.Background(), req)
	} else {
		if decidedBy == "" {
			decidedBy = "auto"
		}
		// Auto-approve
		if err := e.ProcessApproval(ctx, req.ID, "approve", decidedBy); err != nil {
			return nil, err
		}
		// Reload request to reflect updated status
		req, _ = e.requestRepo.GetByID(ctx, req.ID)
	}

	util.Info("Request submitted",
		"request_id", req.ID,
		"operation", operation,
		"expires_at", expiresAt,
	)

	return req, nil
}

// ProcessApproval handles an approval decision.
func (e *Engine) ProcessApproval(ctx context.Context, requestID, action, decidedBy string) error {
	var newStatus string
	switch action {
	case "approve":
		newStatus = database.StatusApproved
	case "deny":
		newStatus = database.StatusDenied
	default:
		return fmt.Errorf("invalid action: %s", action)
	}

	// Atomically update status
	updated, err := e.requestRepo.UpdateStatus(ctx, requestID, newStatus, decidedBy)
	if err != nil {
		return err
	}

	if !updated {
		// Request was already decided
		req, _ := e.requestRepo.GetByID(ctx, requestID)
		if req != nil {
			return fmt.Errorf("request already %s", req.Status)
		}
		return fmt.Errorf("request not found")
	}

	// Log to audit
	auditEvent := database.AuditRequestApproved
	if action == "deny" {
		auditEvent = database.AuditRequestDenied
	}
	e.auditLogger.Log(ctx, auditEvent, requestID, "", decidedBy, nil)

	// If approved, queue for execution
	if action == "approve" {
		e.executionQueue.Enqueue(requestID)
	}

	// Send webhook notification
	go e.notifyWebhook(context.Background(), requestID, newStatus)

	util.Info("Request decision processed",
		"request_id", requestID,
		"action", action,
		"decided_by", decidedBy,
	)

	return nil
}

// ProcessSuggestion handles a change suggestion.
func (e *Engine) ProcessSuggestion(ctx context.Context, requestID, suggestion, suggestedBy string) error {
	if err := e.requestRepo.SetSuggestion(ctx, requestID, suggestion, suggestedBy); err != nil {
		return err
	}

	// Log to audit
	e.auditLogger.Log(ctx, database.AuditRequestChanged, requestID, "", suggestedBy, map[string]interface{}{
		"suggestion": suggestion,
	})

	// Send webhook notification with suggestion
	go e.notifyWebhookWithSuggestion(context.Background(), requestID, suggestion)

	util.Info("Suggestion recorded",
		"request_id", requestID,
		"suggested_by", suggestedBy,
	)

	return nil
}

// GetRequest retrieves a request by ID.
func (e *Engine) GetRequest(ctx context.Context, requestID string) (*database.Request, error) {
	return e.requestRepo.GetByID(ctx, requestID)
}

// CancelRequest cancels a pending request.
func (e *Engine) CancelRequest(ctx context.Context, requestID, apiKeyID string) error {
	if err := e.requestRepo.Cancel(ctx, requestID, apiKeyID); err != nil {
		return err
	}

	e.auditLogger.Log(ctx, database.AuditRequestCancelled, requestID, apiKeyID, "api", nil)

	return nil
}

// ExecuteRequest executes an approved request.
func (e *Engine) ExecuteRequest(ctx context.Context, requestID string) error {
	req, err := e.requestRepo.GetByID(ctx, requestID)
	if err != nil || req == nil {
		return fmt.Errorf("request not found: %s", requestID)
	}

	if req.Status != database.StatusApproved {
		return fmt.Errorf("request is not approved: %s", req.Status)
	}

	// Mark as executing
	if err := e.requestRepo.SetExecuting(ctx, requestID); err != nil {
		return err
	}

	e.auditLogger.Log(ctx, database.AuditRequestExecuting, requestID, req.APIKeyID, "engine", nil)

	// Execute based on operation type
	var result interface{}
	var execErr error

	switch req.Operation {
	case database.OperationCreateEvent:
		result, execErr = e.executeCreateEvent(ctx, req)
	case database.OperationUpdateEvent:
		result, execErr = e.executeUpdateEvent(ctx, req)
	case database.OperationDeleteEvent:
		execErr = e.executeDeleteEvent(ctx, req)
	default:
		execErr = fmt.Errorf("unknown operation: %s", req.Operation)
	}

	if execErr != nil {
		// Check if retryable
		if e.isRetryable(execErr) && req.RetryCount < e.config.Retry.MaxAttempts {
			util.Warn("Request execution failed, will retry",
				"request_id", requestID,
				"error", execErr,
				"retry_count", req.RetryCount,
			)
			e.requestRepo.IncrementRetryCount(ctx, requestID)
			// Re-queue after backoff
			go func() {
				backoff := e.getBackoffDuration(req.RetryCount)
				time.Sleep(backoff)
				e.executionQueue.Enqueue(requestID)
			}()
			return nil
		}

		// Mark as failed
		e.requestRepo.SetError(ctx, requestID, execErr.Error())
		e.auditLogger.Log(ctx, database.AuditRequestFailed, requestID, req.APIKeyID, "engine", map[string]interface{}{
			"error": execErr.Error(),
		})
		go e.notifyWebhook(context.Background(), requestID, database.StatusFailed)
		return execErr
	}

	// Store result
	var resultJSON json.RawMessage
	if result != nil {
		resultJSON, _ = json.Marshal(result)
	}
	if err := e.requestRepo.SetResult(ctx, requestID, resultJSON); err != nil {
		util.Error("Failed to store result", "error", err)
	}

	e.auditLogger.Log(ctx, database.AuditRequestCompleted, requestID, req.APIKeyID, "engine", nil)
	go e.notifyWebhook(context.Background(), requestID, database.StatusCompleted)

	util.Info("Request executed successfully", "request_id", requestID)

	return nil
}

// Helper methods

func (e *Engine) executeCreateEvent(ctx context.Context, req *database.Request) (*google.Event, error) {
	var intent google.EventIntent
	if err := json.Unmarshal(req.Payload, &intent); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	return e.calendarClient.CreateEvent(ctx, &intent)
}

func (e *Engine) executeUpdateEvent(ctx context.Context, req *database.Request) (*google.Event, error) {
	var intent google.EventUpdateIntent
	if err := json.Unmarshal(req.Payload, &intent); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	return e.calendarClient.UpdateEvent(ctx, &intent)
}

func (e *Engine) executeDeleteEvent(ctx context.Context, req *database.Request) error {
	var intent google.EventDeleteIntent
	if err := json.Unmarshal(req.Payload, &intent); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	return e.calendarClient.DeleteEvent(ctx, &intent)
}

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
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return false
}

func (e *Engine) getBackoffDuration(retryCount int) time.Duration {
	if retryCount >= len(e.config.Retry.BackoffSeconds) {
		retryCount = len(e.config.Retry.BackoffSeconds) - 1
	}
	return time.Duration(e.config.Retry.BackoffSeconds[retryCount]) * time.Second
}

func (e *Engine) sendApprovalNotifications(ctx context.Context, req *database.Request) {
	if e.notifier == nil {
		return
	}

	// Create decision token for callbacks if possible
	var decisionToken string
	if e.tokenRepo != nil {
		token, err := e.tokenRepo.Create(ctx, req.ID, req.ExpiresAt)
		if err != nil {
			util.Error("Failed to create decision token", "error", err, "request_id", req.ID)
		} else {
			decisionToken = token
		}
	}

	// Parse payload to get event details
	var details *notifications.EventDetails
	if req.Operation == database.OperationCreateEvent {
		var intent google.EventIntent
		if err := json.Unmarshal(req.Payload, &intent); err == nil {
			details = &notifications.EventDetails{
				Title:       intent.Summary,
				StartTime:   intent.Start,
				EndTime:     intent.End,
				Location:    intent.Location,
				Attendees:   intent.Attendees,
				Description: intent.Description,
			}
		}
	}

	notification := &notifications.ApprovalNotification{
		RequestID: req.ID,
		Operation: req.Operation,
		Summary:   getOperationSummary(req.Operation, details),
		Details:   details,
		ExpiresAt: req.ExpiresAt,
		ExpiresIn: util.GetDefaultFormatter().FormatExpiresIn(req.ExpiresAt),
		DecisionToken: decisionToken,
		// URLs will be set by the notification manager based on config
	}

	if err := e.notifier.SendApprovalRequest(ctx, notification); err != nil {
		util.Error("Failed to send approval notifications", "error", err, "request_id", req.ID)
	}
}

func (e *Engine) notifyWebhook(ctx context.Context, requestID, status string) {
	if e.webhookClient == nil {
		return
	}
	if !e.shouldNotify(status) {
		return
	}

	req, err := e.requestRepo.GetByID(ctx, requestID)
	if err != nil || req == nil {
		return
	}

	event := WebhookEvent{
		RequestID: requestID,
		Status:    status,
		Message:   buildWebhookMessage(req, status),
		Result:    req.Result,
	}

	if err := e.webhookClient.Deliver(ctx, event); err != nil {
		util.Error("Failed to deliver webhook", "error", err, "request_id", requestID)
		return
	}

	e.requestRepo.SetWebhookNotified(ctx, requestID)
}

func (e *Engine) notifyWebhookWithSuggestion(ctx context.Context, requestID, suggestion string) {
	if e.webhookClient == nil {
		return
	}
	if !e.shouldNotify(database.StatusChangeRequested) {
		return
	}

	req, err := e.requestRepo.GetByID(ctx, requestID)
	if err != nil || req == nil {
		return
	}

	event := WebhookEvent{
		RequestID:  requestID,
		Status:     database.StatusChangeRequested,
		Message:    buildSuggestionMessage(req, suggestion),
		Suggestion: suggestion,
	}

	if err := e.webhookClient.Deliver(ctx, event); err != nil {
		util.Error("Failed to deliver webhook", "error", err, "request_id", requestID)
		return
	}

	e.requestRepo.SetWebhookNotified(ctx, requestID)
}

func (e *Engine) shouldNotify(status string) bool {
	if e.webhookClient == nil {
		return false
	}
	if len(e.config.Moltbot.Webhook.NotifyOn) == 0 {
		return true
	}
	for _, allowed := range e.config.Moltbot.Webhook.NotifyOn {
		if allowed == status {
			return true
		}
	}
	return false
}

func getOperationSummary(operation string, details *notifications.EventDetails) string {
	switch operation {
	case database.OperationCreateEvent:
		if details != nil {
			return fmt.Sprintf("Create: %s", details.Title)
		}
		return "Create Event"
	case database.OperationUpdateEvent:
		return "Update Event"
	case database.OperationDeleteEvent:
		return "Delete Event"
	default:
		return operation
	}
}

func buildWebhookMessage(req *database.Request, status string) string {
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
	default:
		return fmt.Sprintf("Calendar request status: %s", status)
	}
}

func buildSuggestionMessage(req *database.Request, suggestion string) string {
	return fmt.Sprintf(`Calendar request needs changes.

Operation: %s
Suggestion: "%s"

Please modify the request based on this feedback and resubmit.`, req.Operation, suggestion)
}
