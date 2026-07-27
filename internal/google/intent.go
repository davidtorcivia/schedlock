// Package google defines the constrained write schema accepted from API
// clients.
package google

import (
	"errors"
	"fmt"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// EventIntent is the schema for event creation.
//
// It is deliberately narrower than Google's own event resource: an agent can
// only express what this struct models, so no unexpected field (guest
// permissions, conferencing, recurrence) can be smuggled through the proxy and
// applied without an approver ever seeing it. Unknown JSON fields are ignored.
type EventIntent struct {
	CalendarID  string     `json:"calendarId"`            // Required: "primary" or a calendar ID
	Summary     string     `json:"summary"`               // Required: event title
	Description string     `json:"description,omitempty"` // Optional
	Location    string     `json:"location,omitempty"`    // Optional
	Start       time.Time  `json:"start"`                 // Required: RFC3339 with offset
	End         time.Time  `json:"end"`                   // Required: RFC3339 with offset
	Attendees   []string   `json:"attendees,omitempty"`   // Optional: email addresses
	ColorID     string     `json:"colorId,omitempty"`     // Optional: "1".."11"
	Visibility  string     `json:"visibility,omitempty"`  // Optional
	Reminders   *Reminders `json:"reminders,omitempty"`   // Optional
}

// Validate reports whether the intent is well-formed and acceptable.
func (e *EventIntent) Validate() error {
	if e.CalendarID == "" {
		return errors.New("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}
	if e.Summary == "" {
		return errors.New("summary is required")
	}
	if e.Start.IsZero() {
		return errors.New("start time is required (RFC3339, e.g. 2024-01-15T10:00:00-05:00)")
	}
	if e.End.IsZero() {
		return errors.New("end time is required (RFC3339, e.g. 2024-01-15T11:00:00-05:00)")
	}
	if err := util.ValidateTimeRange(e.Start, e.End, false); err != nil {
		return err
	}
	if err := validateTextFields(e.Summary, e.Description, e.Location); err != nil {
		return err
	}
	if err := util.ValidateColorID(e.ColorID); err != nil {
		return err
	}
	if err := util.ValidateVisibility(e.Visibility); err != nil {
		return err
	}
	if err := util.ValidateEmails(e.Attendees); err != nil {
		return err
	}
	return e.Reminders.validate()
}

// Sanitize normalizes free-text fields in place.
func (e *EventIntent) Sanitize() {
	e.Summary = util.SanitizeLine(e.Summary)
	e.Location = util.SanitizeLine(e.Location)
	// A description is genuinely multi-line; collapsing it would destroy the
	// agenda or notes an approver is being asked to review.
	e.Description = util.SanitizeText(e.Description)
}

// EventUpdateIntent is the schema for event updates. A nil pointer means
// "leave unchanged", giving the update PATCH semantics.
type EventUpdateIntent struct {
	CalendarID  string     `json:"calendarId"`            // Required
	EventID     string     `json:"eventId"`               // Required
	Summary     *string    `json:"summary,omitempty"`     // Optional
	Description *string    `json:"description,omitempty"` // Optional
	Location    *string    `json:"location,omitempty"`    // Optional
	Start       *time.Time `json:"start,omitempty"`       // Optional
	End         *time.Time `json:"end,omitempty"`         // Optional
	Attendees   []string   `json:"attendees,omitempty"`   // Optional: replaces the list
	ColorID     *string    `json:"colorId,omitempty"`     // Optional
	Visibility  *string    `json:"visibility,omitempty"`  // Optional
	Reminders   *Reminders `json:"reminders,omitempty"`   // Optional
}

// Validate reports whether the update is well-formed.
func (e *EventUpdateIntent) Validate() error {
	if e.CalendarID == "" {
		return errors.New("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}
	if e.EventID == "" {
		return errors.New("eventId is required")
	}
	if e.Start != nil && e.End != nil {
		if err := util.ValidateTimeRange(*e.Start, *e.End, false); err != nil {
			return err
		}
	}
	if err := validateTextFields(deref(e.Summary), deref(e.Description), deref(e.Location)); err != nil {
		return err
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
	if err := util.ValidateEmails(e.Attendees); err != nil {
		return err
	}
	return e.Reminders.validate()
}

// Sanitize normalizes the free-text fields that are present.
func (e *EventUpdateIntent) Sanitize() {
	if e.Summary != nil {
		v := util.SanitizeLine(*e.Summary)
		e.Summary = &v
	}
	if e.Location != nil {
		v := util.SanitizeLine(*e.Location)
		e.Location = &v
	}
	if e.Description != nil {
		v := util.SanitizeText(*e.Description)
		e.Description = &v
	}
}

// HasChanges reports whether the update would modify anything.
func (e *EventUpdateIntent) HasChanges() bool {
	return e.Summary != nil || e.Description != nil || e.Location != nil ||
		e.Start != nil || e.End != nil || len(e.Attendees) > 0 ||
		e.ColorID != nil || e.Visibility != nil || e.Reminders != nil
}

// EventDeleteIntent is the schema for event deletion.
type EventDeleteIntent struct {
	CalendarID string `json:"calendarId"` // Required
	EventID    string `json:"eventId"`    // Required
}

// Validate reports whether the deletion is well-formed.
func (e *EventDeleteIntent) Validate() error {
	if e.CalendarID == "" {
		return errors.New("calendarId is required")
	}
	if err := util.ValidateCalendarID(e.CalendarID); err != nil {
		return err
	}
	if e.EventID == "" {
		return errors.New("eventId is required")
	}
	return nil
}

// validate checks reminder overrides against Google's accepted values.
func (r *Reminders) validate() error {
	if r == nil {
		return nil
	}
	if len(r.Overrides) > 5 {
		return errors.New("at most 5 reminder overrides are allowed")
	}
	for _, override := range r.Overrides {
		if override.Method != "email" && override.Method != "popup" {
			return fmt.Errorf("invalid reminder method %q (must be email or popup)", override.Method)
		}
		if override.Minutes < 0 || override.Minutes > 40320 { // 4 weeks
			return errors.New("reminder minutes must be between 0 and 40320")
		}
	}
	return nil
}

func validateTextFields(summary, description, location string) error {
	if err := util.ValidateLength("summary", summary, util.MaxSummaryLength); err != nil {
		return err
	}
	if err := util.ValidateLength("description", description, util.MaxDescriptionLength); err != nil {
		return err
	}
	return util.ValidateLength("location", location, util.MaxLocationLength)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
