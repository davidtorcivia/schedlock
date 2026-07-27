// Package google provides the calendar types returned to API clients.
package google

import "time"

// Calendar is a calendar the connected account can access.
type Calendar struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
	AccessRole  string `json:"accessRole,omitempty"`
}

// Event is a calendar event.
type Event struct {
	ID          string     `json:"id"`
	Summary     string     `json:"summary"`
	Description string     `json:"description,omitempty"`
	Location    string     `json:"location,omitempty"`
	Start       *EventTime `json:"start,omitempty"`
	End         *EventTime `json:"end,omitempty"`
	Attendees   []Attendee `json:"attendees,omitempty"`
	HTMLLink    string     `json:"htmlLink,omitempty"`
	Status      string     `json:"status,omitempty"`
	Created     *time.Time `json:"created,omitempty"`
	Updated     *time.Time `json:"updated,omitempty"`
	Creator     *Person    `json:"creator,omitempty"`
	Organizer   *Person    `json:"organizer,omitempty"`
	ColorID     string     `json:"colorId,omitempty"`
	Visibility  string     `json:"visibility,omitempty"`
	Reminders   *Reminders `json:"reminders,omitempty"`
}

// EventTime is an event boundary: either a timestamp or an all-day date.
//
// DateTime is a pointer because a struct-typed time.Time cannot be omitted by
// encoding/json, which would render every all-day event with a bogus
// "0001-01-01T00:00:00Z" alongside its date.
type EventTime struct {
	DateTime *time.Time `json:"dateTime,omitempty"`
	Date     string     `json:"date,omitempty"` // YYYY-MM-DD for all-day events
	TimeZone string     `json:"timeZone,omitempty"`
}

// IsAllDay reports whether this boundary is a whole-day date.
func (t *EventTime) IsAllDay() bool {
	return t != nil && t.DateTime == nil && t.Date != ""
}

// Time returns the boundary as a time, resolving all-day dates to midnight.
func (t *EventTime) Time() time.Time {
	if t == nil {
		return time.Time{}
	}
	if t.DateTime != nil {
		return *t.DateTime
	}
	if t.Date != "" {
		loc := time.UTC
		if t.TimeZone != "" {
			if parsed, err := time.LoadLocation(t.TimeZone); err == nil {
				loc = parsed
			}
		}
		if parsed, err := time.ParseInLocation("2006-01-02", t.Date, loc); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// Attendee is an invited participant.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Self           bool   `json:"self,omitempty"`
}

// Person is an event creator or organizer.
type Person struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// Reminders configures event notifications.
type Reminders struct {
	UseDefault bool       `json:"useDefault"`
	Overrides  []Reminder `json:"overrides,omitempty"`
}

// Reminder is a single reminder override.
type Reminder struct {
	Method  string `json:"method"` // "email" or "popup"
	Minutes int    `json:"minutes"`
}

// EventListOptions are the parameters for listing events.
type EventListOptions struct {
	CalendarID   string
	TimeMin      time.Time
	TimeMax      time.Time
	MaxResults   int
	PageToken    string
	Query        string
	SingleEvents bool
	OrderBy      string
}

// EventListResponse is a page of events.
type EventListResponse struct {
	Events        []Event `json:"events"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}

// FreeBusyRequest queries busy intervals across calendars.
type FreeBusyRequest struct {
	TimeMin time.Time          `json:"timeMin"`
	TimeMax time.Time          `json:"timeMax"`
	Items   []FreeBusyCalendar `json:"items"`
}

// FreeBusyCalendar identifies one calendar in a free/busy query.
type FreeBusyCalendar struct {
	ID string `json:"id"`
}

// FreeBusyResponse reports busy intervals per calendar.
type FreeBusyResponse struct {
	TimeMin   time.Time                       `json:"timeMin"`
	TimeMax   time.Time                       `json:"timeMax"`
	Calendars map[string]FreeBusyCalendarInfo `json:"calendars"`
}

// FreeBusyCalendarInfo is one calendar's availability.
type FreeBusyCalendarInfo struct {
	Busy   []TimePeriod `json:"busy"`
	Errors []Error      `json:"errors,omitempty"`
}

// TimePeriod is a busy interval.
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Error is an upstream error reported for a single calendar.
type Error struct {
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}
