// Package crypto provides token generation for decision callbacks.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateDecisionToken creates a secure random token for approval callbacks.
// Returns the token (to be used in URLs) and its hash (to be stored in DB).
// Format: dtok_{base62_encoded_16_bytes}
func GenerateDecisionToken() (token string, hash string, err error) {
	// Generate 16 bytes (128 bits) of random data
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Encode as base62
	encoded, err := bytesToBase62(randomBytes)
	if err != nil {
		return "", "", err
	}

	token = "dtok_" + encoded
	hash = HashSHA256(token)

	return token, hash, nil
}

// GenerateSessionID creates a secure random session ID.
// Returns base64-encoded 32 bytes.
func GenerateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// GenerateCSRFToken creates a secure random CSRF token.
// Returns base64-encoded 32 bytes.
func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// GenerateWebhookID creates a unique ID for webhook delivery tracking.
// Format: whk_{base62_16_chars}
func GenerateWebhookID() (string, error) {
	randomBytes := make([]byte, 12)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate webhook ID: %w", err)
	}

	encoded, err := bytesToBase62(randomBytes)
	if err != nil {
		return "", err
	}

	return "whk_" + encoded, nil
}

// bytesToBase62 converts bytes to base62 string.
func bytesToBase62(data []byte) (string, error) {
	result := make([]byte, len(data)*2) // Approximate size
	idx := 0

	for _, b := range data {
		// Use modulo to map each byte to base62 chars
		// This isn't perfectly uniform but is sufficient for token generation
		result[idx] = base62Chars[b%62]
		idx++
	}

	return string(result[:idx]), nil
}

// GenerateNanoID creates a short unique ID with prefix.
// Used for request IDs, API key IDs, etc.
func GenerateNanoID(prefix string, length int) (string, error) {
	randomBytes := make([]byte, length)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate nanoid: %w", err)
	}

	result := make([]byte, length)
	for i := 0; i < length; i++ {
		result[i] = base62Chars[randomBytes[i]%62]
	}

	return prefix + string(result), nil
}

// Convenience functions for common ID types

// GenerateRequestID creates a request ID (req_ prefix).
func GenerateRequestID() (string, error) {
	return GenerateNanoID("req_", 16)
}

// GenerateAPIKeyID creates an API key ID (key_ prefix).
func GenerateAPIKeyID() (string, error) {
	return GenerateNanoID("key_", 16)
}
