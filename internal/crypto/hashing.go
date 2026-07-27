// Package crypto provides API key generation and HMAC-SHA256 hashing.
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

// base62Alphabet is used for the human-copyable portion of API keys and IDs.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// minSecretBytes is the smallest server secret accepted for key hashing.
const minSecretBytes = 16

// APIKeyHasher generates API keys and hashes them for storage.
type APIKeyHasher struct {
	serverSecret []byte
}

// NewAPIKeyHasher creates a hasher from a base64-encoded or raw server secret.
func NewAPIKeyHasher(secret string) (*APIKeyHasher, error) {
	secretBytes, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		// Not base64: use the raw bytes.
		secretBytes = []byte(secret)
	}

	if len(secretBytes) < minSecretBytes {
		return nil, fmt.Errorf("server secret must decode to at least %d bytes", minSecretBytes)
	}

	return &APIKeyHasher{serverSecret: secretBytes}, nil
}

// GenerateAPIKey creates a new API key for the given tier.
// Format: sk_{tier}_{22 base62 characters} (~130 bits of entropy).
func (h *APIKeyHasher) GenerateAPIKey(tier string) (string, error) {
	switch tier {
	case "read", "write", "admin":
	default:
		return "", fmt.Errorf("invalid tier: %s (must be read, write, or admin)", tier)
	}

	randomPart, err := RandomBase62(22)
	if err != nil {
		return "", fmt.Errorf("failed to generate random part: %w", err)
	}

	return fmt.Sprintf("sk_%s_%s", tier, randomPart), nil
}

// HashAPIKey computes the HMAC-SHA256 of an API key under the server secret.
// Keying the hash means a stolen database cannot be attacked offline without
// also stealing the server secret.
func (h *APIKeyHasher) HashAPIKey(key string) string {
	mac := hmac.New(sha256.New, h.serverSecret)
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyAPIKey reports whether a key matches a stored hash.
func (h *APIKeyHasher) VerifyAPIKey(key, storedHash string) bool {
	return hmac.Equal([]byte(h.HashAPIKey(key)), []byte(storedHash))
}

// GetKeyPrefix returns the non-secret display form of an API key, e.g.
// "sk_write_7kX...zA". Enough to recognise a key, not enough to use it.
func GetKeyPrefix(key string) string {
	if len(key) < 16 {
		return key
	}
	return key[:12] + "..." + key[len(key)-2:]
}

// ParseAPIKeyTier extracts the tier from an API key, or "" if malformed.
func ParseAPIKeyTier(key string) string {
	parts := strings.Split(key, "_")
	if len(parts) != 3 || parts[0] != "sk" {
		return ""
	}

	switch parts[1] {
	case "read", "write", "admin":
		return parts[1]
	default:
		return ""
	}
}

// RandomBase62 returns n cryptographically random base62 characters.
//
// Bytes whose value would wrap the alphabet are rejected rather than reduced
// modulo 62; naive modulo makes the first four characters of the alphabet
// measurably more likely and quietly costs entropy.
func RandomBase62(n int) (string, error) {
	const maxUnbiased = 256 - (256 % len(base62Alphabet)) // 248

	result := make([]byte, 0, n)
	buf := make([]byte, n+n/4+8) // a little slack to cover rejected bytes

	for len(result) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("failed to read random bytes: %w", err)
		}
		for _, b := range buf {
			if int(b) >= maxUnbiased {
				continue
			}
			result = append(result, base62Alphabet[int(b)%len(base62Alphabet)])
			if len(result) == n {
				break
			}
		}
	}

	return string(result), nil
}

// HashSHA256 returns the hex-encoded SHA-256 of data. Used for decision tokens,
// which are already high-entropy random values.
func HashSHA256(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
