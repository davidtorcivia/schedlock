package crypto

import (
	"strings"
	"testing"
)

func TestNewAPIKeyHasher_ValidSecret(t *testing.T) {
	hasher, err := NewAPIKeyHasher("this-is-a-long-enough-secret")
	if err != nil {
		t.Fatalf("NewAPIKeyHasher failed: %v", err)
	}
	if hasher == nil {
		t.Fatal("Hasher is nil")
	}
}

func TestNewAPIKeyHasher_ShortSecret(t *testing.T) {
	_, err := NewAPIKeyHasher("short")
	if err == nil {
		t.Fatal("Expected error for secret < 16 bytes")
	}
}

func TestGenerateAPIKey_ValidTiers(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	tiers := []string{"read", "write", "admin"}
	for _, tier := range tiers {
		key, err := hasher.GenerateAPIKey(tier)
		if err != nil {
			t.Fatalf("GenerateAPIKey(%s) failed: %v", tier, err)
		}

		// Check prefix format
		expectedPrefix := "sk_" + tier + "_"
		if !strings.HasPrefix(key, expectedPrefix) {
			t.Fatalf("Key doesn't have expected prefix: got %q, want prefix %q", key, expectedPrefix)
		}

		// Check total length (sk_ + tier + _ + 22 chars)
		// read: 3+4+1+22 = 30, write: 3+5+1+22 = 31, admin: 3+5+1+22 = 31
		expectedLen := 3 + len(tier) + 1 + 22
		if len(key) != expectedLen {
			t.Fatalf("Key length incorrect: got %d, want %d", len(key), expectedLen)
		}
	}
}

func TestGenerateAPIKey_InvalidTier(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	_, err := hasher.GenerateAPIKey("invalid")
	if err == nil {
		t.Fatal("Expected error for invalid tier")
	}
}

func TestGenerateAPIKey_Uniqueness(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key, _ := hasher.GenerateAPIKey("read")
		if keys[key] {
			t.Fatalf("Generated duplicate key: %s", key)
		}
		keys[key] = true
	}
}

func TestHashAPIKey_Consistency(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	key := "sk_read_12345678901234567890ab"

	hash1 := hasher.HashAPIKey(key)
	hash2 := hasher.HashAPIKey(key)

	if hash1 != hash2 {
		t.Fatal("Same key produced different hashes")
	}

	// Hash should be 64 hex characters (256 bits)
	if len(hash1) != 64 {
		t.Fatalf("Hash length incorrect: got %d, want 64", len(hash1))
	}
}

func TestHashAPIKey_DifferentInputsDifferentHashes(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	hash1 := hasher.HashAPIKey("sk_read_aaaaaaaaaaaaaaaaaaaaaa")
	hash2 := hasher.HashAPIKey("sk_read_bbbbbbbbbbbbbbbbbbbbbb")

	if hash1 == hash2 {
		t.Fatal("Different keys produced same hash")
	}
}

func TestHashAPIKey_DifferentSecretsDifferentHashes(t *testing.T) {
	hasher1, _ := NewAPIKeyHasher("secret-one-12345")
	hasher2, _ := NewAPIKeyHasher("secret-two-12345")

	key := "sk_read_12345678901234567890ab"

	hash1 := hasher1.HashAPIKey(key)
	hash2 := hasher2.HashAPIKey(key)

	if hash1 == hash2 {
		t.Fatal("Same key with different secrets produced same hash")
	}
}

func TestVerifyAPIKey_Valid(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	key, _ := hasher.GenerateAPIKey("write")
	hash := hasher.HashAPIKey(key)

	if !hasher.VerifyAPIKey(key, hash) {
		t.Fatal("VerifyAPIKey returned false for valid key")
	}
}

func TestVerifyAPIKey_Invalid(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	key, _ := hasher.GenerateAPIKey("write")
	hash := hasher.HashAPIKey(key)

	// Modify the key slightly
	wrongKey := key[:len(key)-1] + "X"

	if hasher.VerifyAPIKey(wrongKey, hash) {
		t.Fatal("VerifyAPIKey returned true for invalid key")
	}
}

func TestVerifyAPIKey_WrongHash(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	key, _ := hasher.GenerateAPIKey("read")
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	if hasher.VerifyAPIKey(key, wrongHash) {
		t.Fatal("VerifyAPIKey returned true for wrong hash")
	}
}

func TestGetKeyPrefix(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"sk_write_7kX9mP4qR2sT1uVw8xYzAB", "sk_write_7kX...AB"}, // First 12 + ... + last 2
		{"sk_read_abcdefghijklmnopqrstuv", "sk_read_abcd...uv"},  // First 12 + ... + last 2
		{"short", "short"}, // Too short, return as-is
	}

	for _, tt := range tests {
		result := GetKeyPrefix(tt.key)
		if result != tt.expected {
			t.Errorf("GetKeyPrefix(%q) = %q, want %q", tt.key, result, tt.expected)
		}
	}
}

func TestParseAPIKeyTier(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"sk_read_12345678901234567890ab", "read"},
		{"sk_write_12345678901234567890a", "write"},
		{"sk_admin_12345678901234567890a", "admin"},
		{"sk_invalid_1234567890123456789", ""},     // Invalid tier
		{"invalid", ""},                             // No sk_ prefix
		{"sk_read", ""},                             // Missing random part
		{"sk_read_abc_def", ""},                     // Too many parts
	}

	for _, tt := range tests {
		result := ParseAPIKeyTier(tt.key)
		if result != tt.expected {
			t.Errorf("ParseAPIKeyTier(%q) = %q, want %q", tt.key, result, tt.expected)
		}
	}
}

func TestHashSHA256(t *testing.T) {
	// Test consistency
	hash1 := HashSHA256("test-data")
	hash2 := HashSHA256("test-data")
	if hash1 != hash2 {
		t.Fatal("Same input produced different hashes")
	}

	// Test length
	if len(hash1) != 64 {
		t.Fatalf("Hash length incorrect: got %d, want 64", len(hash1))
	}

	// Test different inputs produce different hashes
	hash3 := HashSHA256("different-data")
	if hash1 == hash3 {
		t.Fatal("Different inputs produced same hash")
	}
}

func TestGenerateBase62_OnlyContainsValidChars(t *testing.T) {
	hasher, _ := NewAPIKeyHasher("test-secret-key-12345")

	// Generate multiple keys and check random part
	for i := 0; i < 50; i++ {
		key, _ := hasher.GenerateAPIKey("read")
		// Extract random part (after sk_read_)
		parts := strings.Split(key, "_")
		randomPart := parts[2]

		for _, c := range randomPart {
			if !strings.ContainsRune(base62Chars, c) {
				t.Fatalf("Random part contains invalid character: %c in %s", c, randomPart)
			}
		}
	}
}
