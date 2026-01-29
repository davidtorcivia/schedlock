package notifications

import (
	"encoding/json"
	"time"
)

// ApprovalNotification contains data for sending approval notifications.
type ApprovalNotification struct {
	RequestID   string
	Operation   string
	Summary     string
	Details     *EventDetails
	ApproveURL  string
	DenyURL     string
	SuggestURL  string
	WebURL      string
	ExpiresAt   time.Time
	ExpiresIn   string
	DecisionToken string
}

// EventDetails contains human-readable event information.
type EventDetails struct {
	Title       string
	StartTime   time.Time
	EndTime     time.Time
	Location    string
	Attendees   []string
	Description string
	CalendarID  string
	EventID     string // For updates/deletes
}

// ResultNotification contains data for result notifications.
type ResultNotification struct {
	RequestID  string
	Operation  string
	Status     string
	Message    string
	EventURL   string
	Error      string
	Result     json.RawMessage
}

// Callback represents an approval callback from a notification provider.
type Callback struct {
	Provider    string
	RequestID   string
	Action      string // "approve", "deny", "suggest"
	Suggestion  string
	MessageID   string
	ChatID      string
	RespondedBy string
}

// NotificationLog represents a logged notification.
type NotificationLog struct {
	ID           int64
	RequestID    string
	Provider     string
	MessageID    string
	SentAt       time.Time
	DeliveredAt  *time.Time
	ErrorMessage string
}
