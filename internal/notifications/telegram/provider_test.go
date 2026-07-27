package telegram

import (
	"strings"
	"testing"
)

// TestEscapeMarkdownCoversEveryReservedCharacter is the regression test for a
// defect that silenced Telegram entirely: MarkdownV2 rejects a message that
// contains an unescaped reserved character, and every notification embedded a
// request ID such as "req_a1b2" plus formatted times containing "." and ",".
// Telegram answered "can't parse entities" and no approval request arrived.
func TestEscapeMarkdownCoversEveryReservedCharacter(t *testing.T) {
	// The set Telegram documents as reserved in MarkdownV2.
	reserved := []string{
		"_", "*", "[", "]", "(", ")", "~", "`", ">",
		"#", "+", "-", "=", "|", "{", "}", ".", "!",
	}

	for _, char := range reserved {
		escaped := escapeMarkdown("a" + char + "b")
		want := "a\\" + char + "b"
		if escaped != want {
			t.Errorf("escapeMarkdown(%q) = %q, want %q", "a"+char+"b", escaped, want)
		}
	}

	// A backslash must be escaped too, or it would escape the next character
	// and shift everything after it.
	if got := escapeMarkdown(`a\b`); got != `a\\b` {
		t.Errorf(`escapeMarkdown("a\b") = %q, want %q`, got, `a\\b`)
	}
}

func TestEscapeMarkdownOnRealisticValues(t *testing.T) {
	cases := []string{
		"req_kX9mP4qzA1b2C3d4",
		"Jan 2, 2026 at 3:04 PM",
		"Q1 review - budget & plans (draft)",
		"Sync w/ team [urgent!]",
		"lunch at Joe's #2",
	}

	for _, value := range cases {
		escaped := escapeMarkdown(value)

		// Every reserved character in the output must be preceded by a
		// backslash. Walking the string is what catches a partial escaper.
		for i := 0; i < len(escaped); i++ {
			c := escaped[i]
			if !isReservedByte(c) {
				continue
			}
			if c == '\\' {
				i++ // skip the character this backslash escapes
				continue
			}
			t.Errorf("escapeMarkdown(%q) left %q unescaped at %d: %q", value, string(c), i, escaped)
			break
		}
	}
}

func TestEscapedTextRoundTripsToOriginal(t *testing.T) {
	original := "Budget review (Q1) - 50% done. Ping @alice!"
	escaped := escapeMarkdown(original)

	if unescaped := strings.ReplaceAll(escaped, `\`, ""); unescaped != original {
		t.Errorf("removing escapes gave %q, want %q", unescaped, original)
	}
}

func isReservedByte(c byte) bool {
	return strings.IndexByte("_*[]()~`>#+-=|{}.!\\", c) >= 0
}

// TestProviderIsInertUntilConfigured checks that a provider registered at
// startup does not attempt delivery before credentials are supplied, which is
// what allows every provider to be registered unconditionally and enabled later
// from the settings page.
func TestProviderIsInertUntilConfigured(t *testing.T) {
	p := NewProvider()

	if p.Enabled() {
		t.Error("a provider with no credentials reports itself enabled")
	}
	if p.WebhookSecret() != "" {
		t.Error("expected no webhook secret before configuration")
	}
	if p.ChatID() != "" {
		t.Error("expected no chat ID before configuration")
	}
}
