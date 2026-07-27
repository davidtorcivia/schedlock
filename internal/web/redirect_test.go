package web

import "testing"

// TestSafeRedirect guards the open redirect on the login page: the "redirect"
// parameter is attacker-supplied, and following an absolute URL would let
// SchedLock bounce a freshly authenticated operator to a look-alike site.
func TestSafeRedirect(t *testing.T) {
	const fallback = "/dashboard"

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"empty falls back", "", fallback},
		{"local path is kept", "/requests/req_abc", "/requests/req_abc"},
		{"local path with query is kept", "/pending?filter=all", "/pending?filter=all"},

		{"absolute http URL is refused", "http://evil.example/pwn", fallback},
		{"absolute https URL is refused", "https://evil.example/pwn", fallback},
		{"protocol-relative URL is refused", "//evil.example/pwn", fallback},
		{"backslash protocol-relative URL is refused", "/\\evil.example/pwn", fallback},
		{"scheme-relative with credentials is refused", "//user:pass@evil.example", fallback},
		{"javascript scheme is refused", "javascript:alert(1)", fallback},
		{"data scheme is refused", "data:text/html,<script>", fallback},
		{"bare host is refused", "evil.example", fallback},

		{"login target would loop", "/login", fallback},
		{"login target with query would loop", "/login?redirect=/x", fallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeRedirect(tt.target, fallback); got != tt.want {
				t.Errorf("SafeRedirect(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}
