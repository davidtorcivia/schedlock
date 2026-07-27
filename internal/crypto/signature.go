// Package crypto provides outbound webhook signing.
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignPayload returns the hex-encoded HMAC-SHA256 of body under secret.
// Receivers verify it by recomputing the same value over the raw request body.
func SignPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
