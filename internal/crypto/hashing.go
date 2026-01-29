// Package crypto provides API key hashing using HMAC-SHA256.
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Base62 alphabet for API key generation
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// APIKeyHasher handles API key generation and HMAC-SHA256 hashing.
type APIKeyHasher struct {
	serverSecret []byte
}

// NewAPIKeyHasher creates a new hasher with the given server secret.
func NewAPIKeyHasher(secret string) (*APIKeyHasher, error) {
	// Decode or use raw secret
	secretBytes, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		// Use raw string if not valid base64
		secretBytes = []byte(secret)
	}

	if len(secretBytes) < 16 {
		return nil, fmt.Errorf("server secret must be at least 16 bytes")
	}

	return &APIKeyHasher{serverSecret: secretBytes}, nil
}

// GenerateAPIKey creates a new API key with the specified tier.
// Returns the full key (to show once) and the key ID.
// Format: sk_{tier}_{22_base62_chars}
func (h *APIKeyHasher) GenerateAPIKey(tier string) (fullKey string, err error) {
	// Validate tier
	if tier != "read" && tier != "write" && tier != "admin" {
		return "", fmt.Errorf("invalid tier: %s (must be read, write, or admin)", tier)
	}

	// Generate 22 base62 characters (~131 bits of entropy)
	randomPart, err := generateBase62(22)
	if err != nil {
		return "", fmt.Errorf("failed to generate random part: %w", err)
	}

	fullKey = fmt.Sprintf("sk_%s_%s", tier, randomPart)
	return fullKey, nil
}

// HashAPIKey computes HMAC-SHA256 hash of an API key.
// Uses server secret as the HMAC key for additional security.
func (h *APIKeyHasher) HashAPIKey(key string) string {
	mac := hmac.New(sha256.New, h.serverSecret)
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyAPIKey checks if a key matches a stored hash.
func (h *APIKeyHasher) VerifyAPIKey(key, storedHash string) bool {
	computedHash := h.HashAPIKey(key)
	return hmac.Equal([]byte(computedHash), []byte(storedHash))
}

// GetKeyPrefix extracts the displayable prefix from an API key.
// Returns first 8 characters + last 2 characters with ellipsis.
// Example: sk_write_7kX9mP4q...zA
func GetKeyPrefix(key string) string {
	if len(key) < 12 {
		return key
	}
	return key[:12] + "..." + key[len(key)-2:]
}

// ParseAPIKeyTier extracts the tier from an API key.
// Returns empty string if format is invalid.
func ParseAPIKeyTier(key string) string {
	if !strings.HasPrefix(key, "sk_") {
		return ""
	}

	parts := strings.Split(key, "_")
	if len(parts) != 3 {
		return ""
	}

	tier := parts[1]
	if tier != "read" && tier != "write" && tier != "admin" {
		return ""
	}

	return tier
}

// generateBase62 generates n random base62 characters.
func generateBase62(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = base62Chars[bytes[i]%62]
	}

	return string(result), nil
}

// HashSHA256 computes a simple SHA-256 hash (for decision tokens).
func HashSHA256(data string) string {
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
