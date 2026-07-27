package webhook

import "encoding/json"

// EventRequestStatus is the event name used for request lifecycle updates.
const EventRequestStatus = "request.status"

// Payload is the JSON body delivered to the calling system.
type Payload struct {
	Event      string          `json:"event"`
	RequestID  string          `json:"request_id"`
	Status     string          `json:"status"`
	Message    string          `json:"message"`
	Suggestion string          `json:"suggestion,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Timestamp  string          `json:"timestamp"`
}
