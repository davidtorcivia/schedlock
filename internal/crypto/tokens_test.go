package crypto

import (
	"strings"
	"testing"
)

func TestGenerateDecisionToken(t *testing.T) {
	token, hash, err := GenerateDecisionToken()
	if err != nil {
		t.Fatalf("GenerateDecisionToken failed: %v", err)
	}

	// Check token format
	if !strings.HasPrefix(token, "dtok_") {
		t.Fatalf("Token doesn't have dtok_ prefix: %s", token)
	}

	// Check hash is non-empty
	if hash == "" {
		t.Fatal("Hash is empty")
	}

	// Hash should be 64 hex chars (SHA256)
	if len(hash) != 64 {
		t.Fatalf("Hash length incorrect: got %d, want 64", len(hash))
	}

	// Verify hash matches token
	expectedHash := HashSHA256(token)
	if hash != expectedHash {
		t.Fatal("Returned hash doesn't match HashSHA256(token)")
	}
}

func TestGenerateDecisionToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)
	hashes := make(map[string]bool)

	for i := 0; i < 100; i++ {
		token, hash, _ := GenerateDecisionToken()
		if tokens[token] {
			t.Fatalf("Generated duplicate token: %s", token)
		}
		if hashes[hash] {
			t.Fatalf("Generated duplicate hash: %s", hash)
		}
		tokens[token] = true
		hashes[hash] = true
	}
}

func TestGenerateSessionID(t *testing.T) {
	sessionID, err := GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID failed: %v", err)
	}

	// Should be base64 URL encoded 32 bytes = 44 chars (no padding with URLEncoding)
	if len(sessionID) != 44 {
		t.Fatalf("Session ID length incorrect: got %d, want 44", len(sessionID))
	}
}

func TestGenerateSessionID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)

	for i := 0; i < 100; i++ {
		id, _ := GenerateSessionID()
		if ids[id] {
			t.Fatalf("Generated duplicate session ID: %s", id)
		}
		ids[id] = true
	}
}

func TestGenerateCSRFToken(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("GenerateCSRFToken failed: %v", err)
	}

	// Should be base64 URL encoded 32 bytes = 44 chars
	if len(token) != 44 {
		t.Fatalf("CSRF token length incorrect: got %d, want 44", len(token))
	}
}

func TestGenerateCSRFToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)

	for i := 0; i < 100; i++ {
		token, _ := GenerateCSRFToken()
		if tokens[token] {
			t.Fatalf("Generated duplicate CSRF token: %s", token)
		}
		tokens[token] = true
	}
}

func TestGenerateWebhookID(t *testing.T) {
	id, err := GenerateWebhookID()
	if err != nil {
		t.Fatalf("GenerateWebhookID failed: %v", err)
	}

	if !strings.HasPrefix(id, "whk_") {
		t.Fatalf("Webhook ID doesn't have whk_ prefix: %s", id)
	}
}

func TestGenerateWebhookID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)

	for i := 0; i < 100; i++ {
		id, _ := GenerateWebhookID()
		if ids[id] {
			t.Fatalf("Generated duplicate webhook ID: %s", id)
		}
		ids[id] = true
	}
}

func TestGenerateNanoID(t *testing.T) {
	tests := []struct {
		prefix string
		length int
	}{
		{"req_", 16},
		{"key_", 16},
		{"test_", 8},
		{"", 12},
	}

	for _, tt := range tests {
		id, err := GenerateNanoID(tt.prefix, tt.length)
		if err != nil {
			t.Fatalf("GenerateNanoID(%q, %d) failed: %v", tt.prefix, tt.length, err)
		}

		if !strings.HasPrefix(id, tt.prefix) {
			t.Fatalf("ID doesn't have expected prefix: got %s, want prefix %s", id, tt.prefix)
		}

		expectedLen := len(tt.prefix) + tt.length
		if len(id) != expectedLen {
			t.Fatalf("ID length incorrect: got %d, want %d", len(id), expectedLen)
		}

		// Check that random part only contains base62 characters
		randomPart := id[len(tt.prefix):]
		for _, c := range randomPart {
			if !strings.ContainsRune(base62Chars, c) {
				t.Fatalf("ID contains invalid character: %c in %s", c, id)
			}
		}
	}
}

func TestGenerateRequestID(t *testing.T) {
	id, err := GenerateRequestID()
	if err != nil {
		t.Fatalf("GenerateRequestID failed: %v", err)
	}

	if !strings.HasPrefix(id, "req_") {
		t.Fatalf("Request ID doesn't have req_ prefix: %s", id)
	}

	// req_ + 16 chars = 20 total
	if len(id) != 20 {
		t.Fatalf("Request ID length incorrect: got %d, want 20", len(id))
	}
}

func TestGenerateAPIKeyID(t *testing.T) {
	id, err := GenerateAPIKeyID()
	if err != nil {
		t.Fatalf("GenerateAPIKeyID failed: %v", err)
	}

	if !strings.HasPrefix(id, "key_") {
		t.Fatalf("API Key ID doesn't have key_ prefix: %s", id)
	}

	// key_ + 16 chars = 20 total
	if len(id) != 20 {
		t.Fatalf("API Key ID length incorrect: got %d, want 20", len(id))
	}
}

func TestBytesToBase62_ValidOutput(t *testing.T) {
	// Test with various byte values
	testData := []byte{0, 61, 62, 123, 255, 1, 100, 200}

	result, err := bytesToBase62(testData)
	if err != nil {
		t.Fatalf("bytesToBase62 failed: %v", err)
	}

	// Check all characters are valid base62
	for _, c := range result {
		if !strings.ContainsRune(base62Chars, c) {
			t.Fatalf("Output contains invalid character: %c", c)
		}
	}
}
