// Package google provides type definitions for Google Calendar API.
package google

import (
	"time"
)

// Calendar represents a Google Calendar.
type Calendar struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
	AccessRole  string `json:"accessRole,omitempty"`
}

// Event represents a Google Calendar event.
type Event struct {
	ID           string     `json:"id"`
	Summary      string     `json:"summary"`
	Description  string     `json:"description,omitempty"`
	Location     string     `json:"location,omitempty"`
	Start        *EventTime `json:"start"`
	End          *EventTime `json:"end"`
	Attendees    []Attendee `json:"attendees,omitempty"`
	HtmlLink     string     `json:"htmlLink,omitempty"`
	Status       string     `json:"status,omitempty"`
	Created      time.Time  `json:"created,omitempty"`
	Updated      time.Time  `json:"updated,omitempty"`
	Creator      *Person    `json:"creator,omitempty"`
	Organizer    *Person    `json:"organizer,omitempty"`
	ColorId      string     `json:"colorId,omitempty"`
	Visibility   string     `json:"visibility,omitempty"`
	Transparency string     `json:"transparency,omitempty"`
	Reminders    *Reminders `json:"reminders,omitempty"`
}

// EventTime represents a time with optional date-only and timezone.
type EventTime struct {
	DateTime time.Time `json:"dateTime,omitempty"`
	Date     string    `json:"date,omitempty"` // YYYY-MM-DD for all-day events
	TimeZone string    `json:"timeZone,omitempty"`
}

// Attendee represents an event attendee.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Self           bool   `json:"self,omitempty"`
}

// Person represents a person (creator/organizer).
type Person struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// Reminders represents event reminders.
type Reminders struct {
	UseDefault bool       `json:"useDefault"`
	Overrides  []Reminder `json:"overrides,omitempty"`
}

// Reminder represents a single reminder.
type Reminder struct {
	Method  string `json:"method"` // "email" or "popup"
	Minutes int    `json:"minutes"`
}

// EventListOptions contains options for listing events.
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

// EventListResponse represents the response from listing events.
type EventListResponse struct {
	Events        []Event `json:"events"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}

// FreeBusyRequest represents a free/busy query request.
type FreeBusyRequest struct {
	TimeMin time.Time           `json:"timeMin"`
	TimeMax time.Time           `json:"timeMax"`
	Items   []FreeBusyCalendar  `json:"items"`
}

// FreeBusyCalendar identifies a calendar in a free/busy query.
type FreeBusyCalendar struct {
	ID string `json:"id"`
}

// FreeBusyResponse represents the response from a free/busy query.
type FreeBusyResponse struct {
	TimeMin   time.Time                    `json:"timeMin"`
	TimeMax   time.Time                    `json:"timeMax"`
	Calendars map[string]FreeBusyCalendarInfo `json:"calendars"`
}

// FreeBusyCalendarInfo contains free/busy info for a calendar.
type FreeBusyCalendarInfo struct {
	Busy   []TimePeriod `json:"busy"`
	Errors []Error      `json:"errors,omitempty"`
}

// TimePeriod represents a busy time period.
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Error represents an API error.
type Error struct {
	Domain  string `json:"domain"`
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// CalendarListResponse represents the response from listing calendars.
type CalendarListResponse struct {
	Calendars     []Calendar `json:"calendars"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}
