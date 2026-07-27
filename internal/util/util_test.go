package util

import (
	"strings"
	"testing"
	"time"
)

// TestParseFromInputUsesDisplayTimezone covers the edit form's round trip. The
// browser submits a datetime-local value with no zone; reading it as UTC while
// rendering it in the operator's timezone shifted every edited event by the
// offset, silently moving meetings.
func TestParseFromInputUsesDisplayTimezone(t *testing.T) {
	formatter, err := NewDisplayFormatter("America/New_York", "", "", "")
	if err != nil {
		t.Fatalf("NewDisplayFormatter failed: %v", err)
	}

	parsed, err := formatter.ParseFromInput("2026-03-14T09:30")
	if err != nil {
		t.Fatalf("ParseFromInput failed: %v", err)
	}

	// 09:30 in New York during daylight saving is 13:30 UTC.
	if got := parsed.UTC().Format("2006-01-02T15:04"); got != "2026-03-14T13:30" {
		t.Errorf("09:30 New York parsed to %s UTC, want 2026-03-14T13:30", got)
	}

	// What was rendered must come back unchanged.
	if rendered := formatter.FormatForInput(parsed); rendered != "2026-03-14T09:30" {
		t.Errorf("round trip produced %q, want %q", rendered, "2026-03-14T09:30")
	}
}

func TestFormatForInputConvertsIntoDisplayTimezone(t *testing.T) {
	formatter, err := NewDisplayFormatter("Asia/Tokyo", "", "", "")
	if err != nil {
		t.Fatalf("NewDisplayFormatter failed: %v", err)
	}

	utc := time.Date(2026, 1, 15, 0, 30, 0, 0, time.UTC)
	if got := formatter.FormatForInput(utc); got != "2026-01-15T09:30" {
		t.Errorf("FormatForInput = %q, want 2026-01-15T09:30", got)
	}
}

func TestSQLiteTimestampRoundTrip(t *testing.T) {
	original := time.Date(2026, 7, 4, 12, 34, 56, 0, time.UTC)

	parsed, err := ParseSQLiteTimestamp(SQLiteTimestamp(original))
	if err != nil {
		t.Fatalf("ParseSQLiteTimestamp failed: %v", err)
	}
	if !parsed.Equal(original) {
		t.Errorf("round trip gave %v, want %v", parsed, original)
	}

	// A local time is normalized to UTC on the way in, matching SQLite's own
	// datetime('now').
	local := original.In(time.FixedZone("test", 5*3600))
	if SQLiteTimestamp(local) != SQLiteTimestamp(original) {
		t.Error("a zoned time was not normalized to UTC")
	}

	if _, err := ParseSQLiteTimestamp("not a timestamp"); err == nil {
		t.Error("expected an error for an unparsable timestamp")
	}
}

// TestSanitizeTextPreservesLineStructure covers a data-loss bug: the single
// sanitizer collapsed all whitespace, so a multi-line event description (an
// agenda, a list of notes) was flattened into one line before the approver
// ever saw it.
func TestSanitizeTextPreservesLineStructure(t *testing.T) {
	input := "Agenda:\r\n- budget\r\n- hiring\n\nNotes: none\x00"
	want := "Agenda:\n- budget\n- hiring\n\nNotes: none"

	if got := SanitizeText(input); got != want {
		t.Errorf("SanitizeText = %q, want %q", got, want)
	}
}

func TestSanitizeLineCollapsesWhitespace(t *testing.T) {
	if got := SanitizeLine("  Weekly   sync\nwith   team  "); got != "Weekly sync with team" {
		t.Errorf("SanitizeLine = %q", got)
	}
}

// TestTruncateStringIsRuneSafe covers byte-slicing truncation, which could cut
// a multi-byte character in half and emit invalid UTF-8 into a notification.
func TestTruncateStringIsRuneSafe(t *testing.T) {
	input := "日本語のイベントタイトル"

	got := TruncateString(input, 5)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("TruncateString = %q, expected an ellipsis", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("TruncateString produced invalid UTF-8: %q", got)
		}
	}

	if got := TruncateString("short", 50); got != "short" {
		t.Errorf("a short string was altered: %q", got)
	}
}

// TestValidateCalendarIDAcceptsRealGoogleIdentifiers covers over-strict
// validation that rejected calendars Google actually issues, so a legitimate
// request was refused before it could ever be reviewed.
func TestValidateCalendarIDAcceptsRealGoogleIdentifiers(t *testing.T) {
	valid := []string{
		"primary",
		"user@example.com",
		"c_abc123@group.calendar.google.com",
		"en.usa#holiday@group.v.calendar.google.com",
		"addressbook#contacts@group.v.calendar.google.com",
		"user+tag@example.co.uk",
	}
	for _, id := range valid {
		if err := ValidateCalendarID(id); err != nil {
			t.Errorf("ValidateCalendarID(%q) rejected a valid ID: %v", id, err)
		}
	}

	invalid := []string{
		"",
		"no-at-sign",
		"user@localhost",
		"user@example.com/../etc",
		"user @example.com",
		"user@example.com\nX-Injected: 1",
	}
	for _, id := range invalid {
		if err := ValidateCalendarID(id); err == nil {
			t.Errorf("ValidateCalendarID(%q) accepted an invalid ID", id)
		}
	}
}

func TestValidateEmailRejectsDisplayNameForms(t *testing.T) {
	if err := ValidateEmail("Alice <alice@example.com>"); err == nil {
		t.Error("a display-name address was accepted as an attendee")
	}
	if err := ValidateEmail("alice@example.com"); err != nil {
		t.Errorf("a plain address was rejected: %v", err)
	}
}

func TestValidateTimeRange(t *testing.T) {
	future := time.Now().Add(time.Hour)

	if err := ValidateTimeRange(future, future.Add(time.Hour), false); err != nil {
		t.Errorf("a valid future range was rejected: %v", err)
	}
	if err := ValidateTimeRange(future, future, false); err == nil {
		t.Error("a zero-length range was accepted")
	}
	if err := ValidateTimeRange(future.Add(time.Hour), future, false); err == nil {
		t.Error("an inverted range was accepted")
	}
	if err := ValidateTimeRange(time.Now().Add(-time.Hour), future, false); err == nil {
		t.Error("a past start was accepted when the past is disallowed")
	}
	if err := ValidateTimeRange(time.Now().Add(-time.Hour), future, true); err != nil {
		t.Errorf("a past start was rejected when the past is allowed: %v", err)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:    "30 seconds",
		time.Minute:         "1 minute",
		45 * time.Minute:    "45 minutes",
		time.Hour:           "1 hour",
		3 * time.Hour:       "3 hours",
		48 * time.Hour:      "2 days",
		time.Hour * 24 * 25: "25 days",
	}

	for input, want := range cases {
		if got := FormatDuration(input); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", input, got, want)
		}
	}
}
