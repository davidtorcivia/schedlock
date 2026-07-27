// Package util provides input validation and normalization.
package util

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Validation errors.
var (
	ErrEmptyField        = errors.New("field cannot be empty")
	ErrInvalidEmail      = errors.New("invalid email address")
	ErrEndBeforeStart    = errors.New("end time must be after start time")
	ErrPastTime          = errors.New("time cannot be in the past")
	ErrInvalidCalendarID = errors.New("invalid calendar ID")
	ErrInvalidColorID    = errors.New("invalid color ID (must be 1-11)")
	ErrInvalidVisibility = errors.New("invalid visibility (must be default, public, or private)")
	ErrTooLong           = errors.New("value is too long")
)

// Field length caps. Google enforces its own limits; these keep an agent from
// filling the database with megabyte payloads before approval is ever sought.
const (
	MaxSummaryLength     = 1024
	MaxDescriptionLength = 8192
	MaxLocationLength    = 1024
	MaxAttendees         = 250
)

// ValidateEmail checks that a string is a usable email address.
func ValidateEmail(email string) error {
	if email == "" {
		return ErrEmptyField
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return ErrInvalidEmail
	}
	// mail.ParseAddress accepts display-name forms ("A <a@b.com>"); calendar
	// attendees must be the bare address.
	if addr.Address != email {
		return ErrInvalidEmail
	}
	return nil
}

// ValidateEmails validates every address in a list.
func ValidateEmails(emails []string) error {
	if len(emails) > MaxAttendees {
		return fmt.Errorf("%w: at most %d attendees", ErrTooLong, MaxAttendees)
	}
	for _, email := range emails {
		if err := ValidateEmail(email); err != nil {
			return fmt.Errorf("invalid attendee %q: %w", email, err)
		}
	}
	return nil
}

// ValidateTimeRange checks ordering and, unless allowPast, rejects start times
// already in the past.
func ValidateTimeRange(start, end time.Time, allowPast bool) error {
	if !allowPast && start.Before(time.Now()) {
		return ErrPastTime
	}
	if !end.After(start) {
		return ErrEndBeforeStart
	}
	return nil
}

// ValidateCalendarID checks that a calendar identifier is plausible.
//
// Google calendar IDs are more varied than an email address: alongside
// "primary" and ordinary addresses there are group calendars
// ("...@group.calendar.google.com"), imported calendars, and locale holiday
// calendars such as "en.usa#holiday@group.v.calendar.google.com". The rule is
// therefore structural rather than an allowlist of shapes.
func ValidateCalendarID(id string) error {
	if id == "" {
		return ErrEmptyField
	}
	if id == "primary" {
		return nil
	}
	if len(id) > 254 || !utf8.ValidString(id) {
		return ErrInvalidCalendarID
	}

	local, domain, found := strings.Cut(id, "@")
	if !found || local == "" || domain == "" {
		return ErrInvalidCalendarID
	}
	if !strings.Contains(domain, ".") {
		return ErrInvalidCalendarID
	}
	for _, r := range id {
		// Control characters and whitespace would let a caller smuggle
		// separators into the URL path built from this value.
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '\\' || r == '?' {
			return ErrInvalidCalendarID
		}
	}
	return nil
}

// ValidateColorID checks a Google Calendar color ID (1-11). Empty is allowed.
func ValidateColorID(colorID string) error {
	if colorID == "" {
		return nil
	}
	switch colorID {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11":
		return nil
	default:
		return ErrInvalidColorID
	}
}

// ValidateVisibility checks an event visibility value. Empty is allowed.
func ValidateVisibility(visibility string) error {
	switch visibility {
	case "", "default", "public", "private":
		return nil
	default:
		return ErrInvalidVisibility
	}
}

// ValidateLength rejects oversized free-text fields.
func ValidateLength(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrTooLong, field, max)
	}
	return nil
}

// SanitizeText normalizes a multi-line free-text field: it trims surrounding
// whitespace, normalizes line endings, and strips control characters, while
// preserving the internal line structure an event description depends on.
func SanitizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// SanitizeLine normalizes a single-line field, collapsing all internal
// whitespace (including newlines) into single spaces.
func SanitizeLine(s string) string {
	return strings.Join(strings.Fields(SanitizeText(s)), " ")
}

// TruncateString shortens s to at most maxLen runes, appending an ellipsis when
// it truncates. It counts runes rather than bytes so multi-byte characters are
// never cut in half.
func TruncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}

	runes := []rune(s)
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return strings.TrimRight(string(runes[:maxLen-3]), " ") + "..."
}
