// Package util provides timezone-aware formatting and SQLite time helpers.
package util

import (
	"fmt"
	"sync/atomic"
	"time"

	// Embed the timezone database so containers without tzdata can still
	// resolve display timezones.
	_ "time/tzdata"
)

// sqliteLayout matches SQLite's datetime('now'), which is always UTC.
const sqliteLayout = "2006-01-02 15:04:05"

// DisplayFormatter renders timestamps in the operator's configured timezone.
// Instances are immutable and safe for concurrent use.
type DisplayFormatter struct {
	location       *time.Location
	dateFormat     string
	timeFormat     string
	datetimeFormat string
}

// NewDisplayFormatter creates a formatter for the given IANA timezone.
// Empty layouts fall back to sensible defaults.
func NewDisplayFormatter(timezone, dateFormat, timeFormat, datetimeFormat string) (*DisplayFormatter, error) {
	if timezone == "" {
		timezone = "UTC"
	}

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
		location:       loc,
		dateFormat:     dateFormat,
		timeFormat:     timeFormat,
		datetimeFormat: datetimeFormat,
	}, nil
}

// Location returns the display timezone. Handlers use it to interpret local
// times submitted by the browser (for example datetime-local form fields).
func (f *DisplayFormatter) Location() *time.Location {
	if f == nil || f.location == nil {
		return time.UTC
	}
	return f.location
}

// FormatDate renders the date portion in the display timezone.
func (f *DisplayFormatter) FormatDate(t time.Time) string {
	return t.In(f.Location()).Format(f.dateFormat)
}

// FormatDateTime renders a full timestamp in the display timezone.
func (f *DisplayFormatter) FormatDateTime(t time.Time) string {
	return t.In(f.Location()).Format(f.datetimeFormat)
}

// FormatForInput renders a timestamp for an HTML datetime-local field, in the
// display timezone so the value the operator sees matches the value they edit.
func (f *DisplayFormatter) FormatForInput(t time.Time) string {
	return t.In(f.Location()).Format("2006-01-02T15:04")
}

// ParseFromInput interprets an HTML datetime-local value as wall-clock time in
// the display timezone. Parsing it as UTC instead silently shifts every edited
// event by the operator's offset.
func (f *DisplayFormatter) ParseFromInput(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02T15:04", value, f.Location())
}

// FormatExpiresIn renders the remaining time before expiry, e.g. "47 minutes".
func (f *DisplayFormatter) FormatExpiresIn(expiresAt time.Time) string {
	remaining := time.Until(expiresAt)
	if remaining <= 0 {
		return "expired"
	}
	return FormatDuration(remaining)
}

// FormatDuration renders a duration in coarse, human units.
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return pluralize(int(d.Seconds()), "second")
	case d < time.Hour:
		return pluralize(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return pluralize(int(d.Hours()), "hour")
	default:
		return pluralize(int(d.Hours()/24), "day")
	}
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// SQLiteTimestamp formats a time for storage in a TEXT column, in UTC to match
// SQLite's own datetime('now').
func SQLiteTimestamp(t time.Time) string {
	return t.UTC().Format(sqliteLayout)
}

// ParseSQLiteTimestamp parses a TEXT timestamp written by SQLite or by
// SQLiteTimestamp. The result is in UTC.
func ParseSQLiteTimestamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	// Values written by application code use the canonical layout; values from
	// older rows or manual edits may carry sub-second precision or a zone.
	for _, layout := range []string{sqliteLayout, "2006-01-02 15:04:05.999999999-07:00", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", s)
}

// defaultFormatter is replaced when display settings change while requests are
// in flight, so it is stored atomically.
var defaultFormatter atomic.Pointer[DisplayFormatter]

func init() {
	f, err := NewDisplayFormatter("UTC", "", "", "")
	if err != nil {
		panic("util: UTC formatter must be constructible: " + err.Error())
	}
	defaultFormatter.Store(f)
}

// SetDefaultFormatter replaces the package-level display formatter.
func SetDefaultFormatter(f *DisplayFormatter) {
	if f != nil {
		defaultFormatter.Store(f)
	}
}

// GetDefaultFormatter returns the package-level display formatter.
func GetDefaultFormatter() *DisplayFormatter {
	return defaultFormatter.Load()
}
