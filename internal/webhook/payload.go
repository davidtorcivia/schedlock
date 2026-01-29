package webhook

import "encoding/json"

// WebhookPayload represents the payload sent to Moltbot.
type WebhookPayload struct {
	Event      string          `json:"event"`
	RequestID  string          `json:"request_id"`
	Status     string          `json:"status"`
	Message    string          `json:"message"`
	Suggestion string          `json:"suggestion,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Timestamp  string          `json:"timestamp"`
}

// Event types for webhooks.
const (
	EventRequestStatus   = "request.status"
	EventRequestApproved = "request.approved"
	EventRequestDenied   = "request.denied"
	EventRequestExpired  = "request.expired"
	EventRequestComplete = "request.completed"
	EventRequestFailed   = "request.failed"
	EventSuggestion      = "request.suggestion"
)
