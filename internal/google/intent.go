// Package google provides the EventIntent schema for write operations.
package google

import (
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// EventIntent represents the constrained schema for event creation/update.
// Unknown fields from API requests are silently ignored for security.
type EventIntent struct {
	CalendarID  string     `json:"calendarId"`            // Required: "primary" or calendar ID
	Summary     string     `json:"summary"`               // Required: Event title
	Description string     `json:"description,omitempty"` // Optional: Event description
	Location    string     `json:"location,omitempty"`    // Optional: Location text
	Start       time.Time  `json:"start"`                 // Required: RFC3339 with timezone
	End         time.Time  `json:"end"`                   // Required: RFC3339 with timezone
	Attendees   []string   `json:"attendees,omitempty"`   // Optional: Email addresses
	ColorID     string     `json:"colorId,omitempty"`     // Optional: Event color (1-11)
	Visibility  string     `json:"visibility,omitempty"`  // Optional: "default", "public", "private"
	Reminders   *Reminders `json:"reminders,omitempty"`   // Optional: Custom reminders
}

// Validate checks if the EventIntent has all required fields and valid values.
func (e *EventIntent) Validate() error {
	if e.CalendarID == "" {
		return fmt.Errorf("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}

	if e.Summary == "" {
		return fmt.Errorf("summary is required")
	}

	if e.Start.IsZero() {
		return fmt.Errorf("start time is required")
	}

	if e.End.IsZero() {
		return fmt.Errorf("end time is required")
	}

	if err := util.ValidateTimeRange(e.Start, e.End, false); err != nil {
		return err
	}

	if e.ColorID != "" {
		if err := util.ValidateColorID(e.ColorID); err != nil {
			return err
		}
	}

	if e.Visibility != "" {
		if err := util.ValidateVisibility(e.Visibility); err != nil {
			return err
		}
	}

	if len(e.Attendees) > 0 {
		if err := util.ValidateEmails(e.Attendees); err != nil {
			return err
		}
	}

	return nil
}

// Sanitize cleans and normalizes the EventIntent fields.
func (e *EventIntent) Sanitize() {
	e.Summary = util.SanitizeString(e.Summary)
	e.Description = util.SanitizeString(e.Description)
	e.Location = util.SanitizeString(e.Location)
}

// EventUpdateIntent represents the schema for event updates.
// Only provided fields will be updated (PATCH semantics).
type EventUpdateIntent struct {
	CalendarID  string     `json:"calendarId"`            // Required: "primary" or calendar ID
	EventID     string     `json:"eventId"`               // Required: Event to update
	Summary     *string    `json:"summary,omitempty"`     // Optional: New title
	Description *string    `json:"description,omitempty"` // Optional: New description
	Location    *string    `json:"location,omitempty"`    // Optional: New location
	Start       *time.Time `json:"start,omitempty"`       // Optional: New start time
	End         *time.Time `json:"end,omitempty"`         // Optional: New end time
	Attendees   []string   `json:"attendees,omitempty"`   // Optional: Replace attendees
	ColorID     *string    `json:"colorId,omitempty"`     // Optional: New color
	Visibility  *string    `json:"visibility,omitempty"`  // Optional: New visibility
	Reminders   *Reminders `json:"reminders,omitempty"`   // Optional: New reminders
}

// Validate checks if the EventUpdateIntent has all required fields and valid values.
func (e *EventUpdateIntent) Validate() error {
	if e.CalendarID == "" {
		return fmt.Errorf("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}

	if e.EventID == "" {
		return fmt.Errorf("eventId is required")
	}

	// Validate optional fields if provided
	if e.Start != nil && e.End != nil {
		if err := util.ValidateTimeRange(*e.Start, *e.End, false); err != nil {
			return err
		}
	}

	if e.ColorID != nil {
		if err := util.ValidateColorID(*e.ColorID); err != nil {
			return err
		}
	}

	if e.Visibility != nil {
		if err := util.ValidateVisibility(*e.Visibility); err != nil {
			return err
		}
	}

	if len(e.Attendees) > 0 {
		if err := util.ValidateEmails(e.Attendees); err != nil {
			return err
		}
	}

	return nil
}

// HasChanges checks if any fields are set for update.
func (e *EventUpdateIntent) HasChanges() bool {
	return e.Summary != nil || e.Description != nil || e.Location != nil ||
		e.Start != nil || e.End != nil || len(e.Attendees) > 0 ||
		e.ColorID != nil || e.Visibility != nil || e.Reminders != nil
}

// EventDeleteIntent represents the schema for event deletion.
type EventDeleteIntent struct {
	CalendarID string `json:"calendarId"` // Required: "primary" or calendar ID
	EventID    string `json:"eventId"`    // Required: Event to delete
}

// Validate checks if the EventDeleteIntent has all required fields.
func (e *EventDeleteIntent) Validate() error {
	if e.CalendarID == "" {
		return fmt.Errorf("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}

	if e.EventID == "" {
		return fmt.Errorf("eventId is required")
	}

	return nil
}

// Diff represents the changes between two EventIntents for display.
type Diff struct {
	Field    string `json:"field"`
	OldValue string `json:"oldValue"`
	NewValue string `json:"newValue"`
}

// GenerateDiff creates a list of changes between an existing event and an update.
func GenerateDiff(existing *Event, update *EventUpdateIntent) []Diff {
	var diffs []Diff

	if update.Summary != nil && *update.Summary != existing.Summary {
		diffs = append(diffs, Diff{
			Field:    "Summary",
			OldValue: existing.Summary,
			NewValue: *update.Summary,
		})
	}

	if update.Description != nil && *update.Description != existing.Description {
		diffs = append(diffs, Diff{
			Field:    "Description",
			OldValue: util.TruncateString(existing.Description, 50),
			NewValue: util.TruncateString(*update.Description, 50),
		})
	}

	if update.Location != nil && *update.Location != existing.Location {
		diffs = append(diffs, Diff{
			Field:    "Location",
			OldValue: existing.Location,
			NewValue: *update.Location,
		})
	}

	if update.Start != nil && existing.Start != nil {
		if !update.Start.Equal(existing.Start.DateTime) {
			diffs = append(diffs, Diff{
				Field:    "Start",
				OldValue: existing.Start.DateTime.Format(time.RFC3339),
				NewValue: update.Start.Format(time.RFC3339),
			})
		}
	}

	if update.End != nil && existing.End != nil {
		if !update.End.Equal(existing.End.DateTime) {
			diffs = append(diffs, Diff{
				Field:    "End",
				OldValue: existing.End.DateTime.Format(time.RFC3339),
				NewValue: update.End.Format(time.RFC3339),
			})
		}
	}

	// TODO: Attendee diff comparison

	return diffs
}
