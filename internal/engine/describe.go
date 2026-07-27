package engine

import (
	"encoding/json"
	"fmt"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
)

// DescribeRequest turns a stored request payload into the human-readable
// details shown to an approver.
//
// This is the single interpretation of a stored payload. Notifications, the
// public approval page, and the request detail page all render from it, so an
// approver sees the same description of an operation wherever they meet it.
func DescribeRequest(req *database.Request) *notifications.EventDetails {
	if req == nil {
		return nil
	}

	switch req.Operation {
	case database.OperationCreateEvent:
		var intent google.EventIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil
		}
		return &notifications.EventDetails{
			Title:       intent.Summary,
			StartTime:   intent.Start,
			EndTime:     intent.End,
			Location:    intent.Location,
			Attendees:   intent.Attendees,
			Description: intent.Description,
			CalendarID:  intent.CalendarID,
		}

	case database.OperationUpdateEvent:
		var intent google.EventUpdateIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil
		}
		details := &notifications.EventDetails{
			Attendees:  intent.Attendees,
			CalendarID: intent.CalendarID,
			EventID:    intent.EventID,
			IsPartial:  true,
		}
		if intent.Summary != nil {
			details.Title = *intent.Summary
		}
		if intent.Location != nil {
			details.Location = *intent.Location
		}
		if intent.Description != nil {
			details.Description = *intent.Description
		}
		if intent.Start != nil {
			details.StartTime = *intent.Start
		}
		if intent.End != nil {
			details.EndTime = *intent.End
		}
		return details

	case database.OperationDeleteEvent:
		var intent google.EventDeleteIntent
		if err := json.Unmarshal(req.Payload, &intent); err != nil {
			return nil
		}
		return &notifications.EventDetails{
			CalendarID: intent.CalendarID,
			EventID:    intent.EventID,
		}

	default:
		return nil
	}
}

// SummarizeOperation produces the one-line summary used as a notification
// title.
func SummarizeOperation(operation string, details *notifications.EventDetails) string {
	title := ""
	if details != nil {
		title = details.Title
	}

	switch operation {
	case database.OperationCreateEvent:
		if title != "" {
			return fmt.Sprintf("Create: %s", title)
		}
		return "Create event"
	case database.OperationUpdateEvent:
		if title != "" {
			return fmt.Sprintf("Update: %s", title)
		}
		return "Update event"
	case database.OperationDeleteEvent:
		return "Delete event"
	default:
		return operation
	}
}
