// Package util provides utility functions for the application.
package util

import (
	"fmt"
	"time"
	// Embed timezone database for containers without tzdata
	_ "time/tzdata"
)

// DisplayFormatter handles timezone-aware display formatting.
type DisplayFormatter struct {
	Location       *time.Location
	DateFormat     string
	TimeFormat     string
	DatetimeFormat string
}

// NewDisplayFormatter creates a formatter for the specified timezone.
func NewDisplayFormatter(timezone string, dateFormat, timeFormat, datetimeFormat string) (*DisplayFormatter, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}

	if dateFormat == "" {
		dateFormat = "Jan 2, 2006"
	}
	if timeFormat == "" {
		timeFormat = "3:04 PM"
	}
	if datetimeFormat == "" {
		datetimeFormat = "Jan 2, 2006 at 3:04 PM"
	}

	return &DisplayFormatter{
		Location:       loc,
		DateFormat:     dateFormat,
		TimeFormat:     timeFormat,
		DatetimeFormat: datetimeFormat,
	}, nil
}

// FormatDate formats a time as date only in local timezone.
func (f *DisplayFormatter) FormatDate(t time.Time) string {
	return t.In(f.Location).Format(f.DateFormat)
}

// FormatTime formats a time as time only in local timezone.
func (f *DisplayFormatter) FormatTime(t time.Time) string {
	return t.In(f.Location).Format(f.TimeFormat)
}

// FormatDateTime formats a time as full datetime in local timezone.
func (f *DisplayFormatter) FormatDateTime(t time.Time) string {
	return t.In(f.Location).Format(f.DatetimeFormat)
}

// FormatDateTimeWithZone formats with timezone abbreviation.
func (f *DisplayFormatter) FormatDateTimeWithZone(t time.Time) string {
	local := t.In(f.Location)
	zone, _ := local.Zone()
	return local.Format(f.DatetimeFormat) + " " + zone
}

// FormatRelative formats a time relative to now (e.g., "in 47 minutes", "2 hours ago").
func (f *DisplayFormatter) FormatRelative(t time.Time) string {
	now := time.Now()
	diff := t.Sub(now)

	if diff < 0 {
		// Past
		diff = -diff
		return formatDuration(diff) + " ago"
	}
	// Future
	return "in " + formatDuration(diff)
}

// FormatExpiresIn formats expiry time for notifications.
func (f *DisplayFormatter) FormatExpiresIn(expiresAt time.Time) string {
	diff := time.Until(expiresAt)
	if diff <= 0 {
		return "expired"
	}
	return formatDuration(diff)
}

// formatDuration converts a duration to human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		secs := int(d.Seconds())
		if secs == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", secs)
	}

	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}

	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}

	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// ParseRFC3339 parses an RFC3339 timestamp.
func ParseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// FormatRFC3339 formats a time as RFC3339.
func FormatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// NowUTC returns current time in UTC.
func NowUTC() time.Time {
	return time.Now().UTC()
}

// SQLiteTimestamp formats a time for SQLite (ISO8601).
func SQLiteTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// ParseSQLiteTimestamp parses a SQLite timestamp.
func ParseSQLiteTimestamp(s string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05", s)
}

// Default formatter instance
var defaultFormatter *DisplayFormatter

func init() {
	// Create a default formatter with UTC timezone
	defaultFormatter, _ = NewDisplayFormatter("UTC", "", "", "")
}

// SetDefaultFormatter sets the global default formatter.
func SetDefaultFormatter(f *DisplayFormatter) {
	defaultFormatter = f
}

// GetDefaultFormatter returns the global default formatter.
func GetDefaultFormatter() *DisplayFormatter {
	return defaultFormatter
}
