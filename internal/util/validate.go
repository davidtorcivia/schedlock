// Package util provides input validation utilities.
package util

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// Validation errors
var (
	ErrEmptyField       = fmt.Errorf("field cannot be empty")
	ErrInvalidEmail     = fmt.Errorf("invalid email address")
	ErrInvalidTime      = fmt.Errorf("invalid time format (expected RFC3339)")
	ErrEndBeforeStart   = fmt.Errorf("end time must be after start time")
	ErrPastTime         = fmt.Errorf("time cannot be in the past")
	ErrInvalidCalendarID = fmt.Errorf("invalid calendar ID")
	ErrInvalidColorID   = fmt.Errorf("invalid color ID (must be 1-11)")
	ErrInvalidVisibility = fmt.Errorf("invalid visibility (must be default, public, or private)")
	ErrDurationTooLong  = fmt.Errorf("event duration exceeds maximum allowed")
	ErrTooManyAttendees = fmt.Errorf("too many attendees")
)

// calendarIDRegex matches valid Google Calendar IDs
var calendarIDRegex = regexp.MustCompile(`^(primary|[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}|[a-z0-9]+@group\.calendar\.google\.com)$`)

// ValidateEmail checks if a string is a valid email address.
func ValidateEmail(email string) error {
	if email == "" {
		return ErrEmptyField
	}

	_, err := mail.ParseAddress(email)
	if err != nil {
		return ErrInvalidEmail
	}

	return nil
}

// ValidateEmails validates a list of email addresses.
func ValidateEmails(emails []string) error {
	for _, email := range emails {
		if err := ValidateEmail(email); err != nil {
			return fmt.Errorf("invalid email %q: %w", email, err)
		}
	}
	return nil
}

// ValidateEmailDomain checks if an email belongs to allowed domains.
func ValidateEmailDomain(email string, allowedDomains []string) error {
	if len(allowedDomains) == 0 {
		return nil // No restrictions
	}

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ErrInvalidEmail
	}

	domain := strings.ToLower(parts[1])
	for _, allowed := range allowedDomains {
		if strings.ToLower(allowed) == domain {
			return nil
		}
	}

	return fmt.Errorf("email domain %q not in allowed list", domain)
}

// ValidateTimeRange validates start and end times.
func ValidateTimeRange(start, end time.Time, allowPast bool) error {
	if !allowPast && start.Before(time.Now()) {
		return ErrPastTime
	}

	if !end.After(start) {
		return ErrEndBeforeStart
	}

	return nil
}

// ValidateDuration checks if duration is within limits.
func ValidateDuration(start, end time.Time, maxMinutes int) error {
	if maxMinutes <= 0 {
		return nil // No limit
	}

	duration := end.Sub(start)
	maxDuration := time.Duration(maxMinutes) * time.Minute

	if duration > maxDuration {
		return fmt.Errorf("%w: %v exceeds %d minutes", ErrDurationTooLong, duration, maxMinutes)
	}

	return nil
}

// ValidateCalendarID checks if a calendar ID is valid.
func ValidateCalendarID(id string) error {
	if id == "" {
		return ErrEmptyField
	}

	if !calendarIDRegex.MatchString(id) {
		return ErrInvalidCalendarID
	}

	return nil
}

// ValidateColorID checks if a color ID is valid (1-11).
func ValidateColorID(colorID string) error {
	if colorID == "" {
		return nil // Optional
	}

	valid := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}
	for _, v := range valid {
		if colorID == v {
			return nil
		}
	}

	return ErrInvalidColorID
}

// ValidateVisibility checks if visibility value is valid.
func ValidateVisibility(visibility string) error {
	if visibility == "" {
		return nil // Optional, defaults to "default"
	}

	valid := []string{"default", "public", "private"}
	for _, v := range valid {
		if visibility == v {
			return nil
		}
	}

	return ErrInvalidVisibility
}

// ValidateAttendeeCount checks if attendee count is within limits.
func ValidateAttendeeCount(count, max int) error {
	if max <= 0 {
		return nil // No limit
	}

	if count > max {
		return fmt.Errorf("%w: %d exceeds maximum of %d", ErrTooManyAttendees, count, max)
	}

	return nil
}

// SanitizeString removes leading/trailing whitespace and normalizes internal whitespace.
func SanitizeString(s string) string {
	// Trim
	s = strings.TrimSpace(s)
	// Normalize internal whitespace
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// TruncateString truncates a string to max length, adding ellipsis if needed.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
