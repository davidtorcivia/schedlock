// Package crypto provides identifier and token generation.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateDecisionToken creates a single-use approval token and the hash to be
// stored alongside the request. The token carries ~155 bits of entropy, so it
// cannot be guessed by anyone holding an approval URL's shape.
func GenerateDecisionToken() (token string, hash string, err error) {
	random, err := RandomBase62(26)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate decision token: %w", err)
	}

	token = "dtok_" + random
	return token, HashSHA256(token), nil
}

// GenerateSessionID creates a session identifier (256 bits, URL-safe).
func GenerateSessionID() (string, error) {
	return randomURLSafe(32)
}

// GenerateCSRFToken creates a CSRF token (256 bits, URL-safe).
func GenerateCSRFToken() (string, error) {
	return randomURLSafe(32)
}

// GenerateWebhookID creates an identifier for a webhook delivery attempt.
func GenerateWebhookID() (string, error) {
	return GenerateNanoID("whk_", 16)
}

// GenerateRequestID creates a request identifier.
func GenerateRequestID() (string, error) {
	return GenerateNanoID("req_", 16)
}

// GenerateAPIKeyID creates an API key identifier.
func GenerateAPIKeyID() (string, error) {
	return GenerateNanoID("key_", 16)
}

// GenerateNanoID creates a prefixed identifier with length random base62
// characters.
func GenerateNanoID(prefix string, length int) (string, error) {
	random, err := RandomBase62(length)
	if err != nil {
		return "", fmt.Errorf("failed to generate id: %w", err)
	}
	return prefix + random, nil
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
