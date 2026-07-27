package web

import (
	"net/url"
	"strings"
)

// SafeRedirect validates a caller-supplied redirect target, returning fallback
// when the target is not a local path.
//
// The login page carries the visitor's original destination in a query
// parameter. Following it unchecked turns the sign-in page into an open
// redirector: an attacker can send "…/login?redirect=https://evil.example" and
// have SchedLock itself bounce the freshly authenticated operator to a
// look-alike site. Only same-site paths are accepted.
func SafeRedirect(target, fallback string) string {
	if target == "" {
		return fallback
	}

	// "//host" and "/\host" are protocol-relative: browsers treat them as
	// absolute URLs even though they begin with a slash.
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
		return fallback
	}

	parsed, err := url.Parse(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Scheme != "" {
		return fallback
	}

	// A redirect back to the login page would loop.
	if strings.HasPrefix(parsed.Path, "/login") {
		return fallback
	}

	return parsed.RequestURI()
}
