package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/google"
)

// TestDescribeRequestReadsStoredIntents is the regression test for the defect
// that mattered most: the approval page parsed the stored payload as if it were
// a Google API event resource ("start.dateTime", "attendees[].email"), while
// requests are actually stored as the proxy's own intent schema ("start" as an
// RFC3339 string, "attendees" as plain addresses).
//
// Every field silently came back empty, so the person being asked to approve a
// calendar change saw no time, no attendees, and no location.
func TestDescribeRequestReadsStoredIntents(t *testing.T) {
	start := time.Date(2026, 3, 14, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	t.Run("create", func(t *testing.T) {
		payload := mustJSON(t, google.EventIntent{
			CalendarID:  "primary",
			Summary:     "Quarterly review",
			Description: "Agenda:\n- numbers\n- plans",
			Location:    "Room 4",
			Start:       start,
			End:         end,
			Attendees:   []string{"a@example.com", "b@example.com"},
		})

		details := DescribeRequest(&database.Request{
			Operation: database.OperationCreateEvent,
			Payload:   payload,
		})

		if details == nil {
			t.Fatal("expected details for a create request")
		}
		if details.Title != "Quarterly review" {
			t.Errorf("Title = %q, want %q", details.Title, "Quarterly review")
		}
		if !details.StartTime.Equal(start) {
			t.Errorf("StartTime = %v, want %v", details.StartTime, start)
		}
		if !details.EndTime.Equal(end) {
			t.Errorf("EndTime = %v, want %v", details.EndTime, end)
		}
		if details.Location != "Room 4" {
			t.Errorf("Location = %q, want %q", details.Location, "Room 4")
		}
		if len(details.Attendees) != 2 {
			t.Errorf("Attendees = %v, want 2 entries", details.Attendees)
		}
		if details.IsPartial {
			t.Error("a create request is not a partial update")
		}
	})

	t.Run("update", func(t *testing.T) {
		newTitle := "Moved: quarterly review"
		payload := mustJSON(t, google.EventUpdateIntent{
			CalendarID: "primary",
			EventID:    "evt_123",
			Summary:    &newTitle,
			Start:      &start,
			End:        &end,
		})

		details := DescribeRequest(&database.Request{
			Operation: database.OperationUpdateEvent,
			Payload:   payload,
		})

		if details == nil {
			t.Fatal("expected details for an update request")
		}
		if details.Title != newTitle {
			t.Errorf("Title = %q, want %q", details.Title, newTitle)
		}
		if !details.StartTime.Equal(start) {
			t.Errorf("StartTime = %v, want %v", details.StartTime, start)
		}
		if details.EventID != "evt_123" {
			t.Errorf("EventID = %q, want %q", details.EventID, "evt_123")
		}
		if !details.IsPartial {
			t.Error("an update leaves unmentioned fields alone and must say so")
		}
	})

	t.Run("delete", func(t *testing.T) {
		payload := mustJSON(t, google.EventDeleteIntent{
			CalendarID: "team@group.calendar.google.com",
			EventID:    "evt_456",
		})

		details := DescribeRequest(&database.Request{
			Operation: database.OperationDeleteEvent,
			Payload:   payload,
		})

		if details == nil {
			t.Fatal("expected details for a delete request")
		}
		if details.EventID != "evt_456" {
			t.Errorf("EventID = %q, want %q", details.EventID, "evt_456")
		}
		if details.CalendarID != "team@group.calendar.google.com" {
			t.Errorf("CalendarID = %q", details.CalendarID)
		}
	})

	t.Run("unreadable payload", func(t *testing.T) {
		details := DescribeRequest(&database.Request{
			Operation: database.OperationCreateEvent,
			Payload:   json.RawMessage(`{"start": "not-a-time"}`),
		})
		if details != nil {
			t.Errorf("expected nil details for an unreadable payload, got %+v", details)
		}
	})
}

func TestSummarizeOperation(t *testing.T) {
	details := DescribeRequest(&database.Request{
		Operation: database.OperationCreateEvent,
		Payload: mustJSON(t, google.EventIntent{
			Summary: "Standup",
		}),
	})

	if got := SummarizeOperation(database.OperationCreateEvent, details); got != "Create: Standup" {
		t.Errorf("SummarizeOperation = %q, want %q", got, "Create: Standup")
	}
	if got := SummarizeOperation(database.OperationDeleteEvent, nil); got != "Delete event" {
		t.Errorf("SummarizeOperation = %q, want %q", got, "Delete event")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to encode test payload: %v", err)
	}
	return data
}
