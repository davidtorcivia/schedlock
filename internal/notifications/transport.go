package notifications

import (
	"io"
	"net/http"
	"time"
)

// MaxProviderResponseBytes caps how much of a provider response is read.
// Provider replies are small JSON acknowledgements; reading without a bound
// would let a hostile or malfunctioning endpoint stream memory away.
const MaxProviderResponseBytes = 64 << 10

// DefaultProviderTimeout bounds a single delivery attempt.
const DefaultProviderTimeout = 30 * time.Second

// NewHTTPClient returns a client suitable for provider delivery.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultProviderTimeout
	}
	return &http.Client{Timeout: timeout}
}

// ReadLimited reads a bounded prefix of a response body.
func ReadLimited(r io.Reader) []byte {
	data, _ := io.ReadAll(io.LimitReader(r, MaxProviderResponseBytes))
	return data
}
