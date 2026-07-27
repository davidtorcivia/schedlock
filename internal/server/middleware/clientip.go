package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver derives the originating client address for logging and for
// per-IP login throttling.
//
// X-Forwarded-For and X-Real-IP are set by whoever speaks to the server, so
// they are honoured only when the immediate peer is a configured trusted proxy.
// Trusting them unconditionally lets any client rotate a header value to defeat
// login rate limiting.
type ClientIPResolver struct {
	trusted []netipPrefix
}

type netipPrefix struct {
	network *net.IPNet
	single  net.IP
}

// NewClientIPResolver builds a resolver from CIDR blocks or bare addresses.
// Unparsable entries are reported and ignored.
func NewClientIPResolver(trustedProxies []string) (*ClientIPResolver, []string) {
	var (
		resolver ClientIPResolver
		invalid  []string
	)

	for _, entry := range trustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			resolver.trusted = append(resolver.trusted, netipPrefix{network: network})
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			resolver.trusted = append(resolver.trusted, netipPrefix{single: ip})
			continue
		}
		invalid = append(invalid, entry)
	}

	return &resolver, invalid
}

// ClientIP returns the best available client address for the request.
func (c *ClientIPResolver) ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	peer := remoteAddrIP(r)
	if !c.trusts(peer) {
		return peer
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The left-most entry is the original client as recorded by the first
		// proxy in the chain.
		if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
			return strings.TrimSpace(first)
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	return peer
}

// trusts reports whether an address belongs to a configured proxy.
func (c *ClientIPResolver) trusts(addr string) bool {
	if len(c.trusted) == 0 {
		return false
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, t := range c.trusted {
		if t.network != nil && t.network.Contains(ip) {
			return true
		}
		if t.single != nil && t.single.Equal(ip) {
			return true
		}
	}
	return false
}

func remoteAddrIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
