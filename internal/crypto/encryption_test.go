package crypto

import (
	"encoding/base64"
	"testing"
)

func TestNewEncryptor_Base64Key(t *testing.T) {
	// Generate a valid 32-byte key encoded as base64
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)

	enc, err := NewEncryptor(encodedKey)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}
	if enc == nil {
		t.Fatal("Encryptor is nil")
	}
}

func TestNewEncryptor_SecretString(t *testing.T) {
	// Use a plain string that will be derived via HKDF
	enc, err := NewEncryptor("my-secret-password")
	if err != nil {
		t.Fatalf("NewEncryptor with secret failed: %v", err)
	}
	if enc == nil {
		t.Fatal("Encryptor is nil")
	}
}

func TestEncryptDecrypt_Basic(t *testing.T) {
	enc, err := NewEncryptor("test-encryption-key-12345")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	plaintext := "my-super-secret-oauth-token"

	// Encrypt
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if len(ciphertext) == 0 {
		t.Fatal("Ciphertext is empty")
	}

	// Ciphertext should be different from plaintext
	if string(ciphertext) == plaintext {
		t.Fatal("Ciphertext equals plaintext - encryption did not work")
	}

	// Decrypt
	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted text doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	plaintext := ""

	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt empty string failed: %v", err)
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt empty string failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted empty string doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_UnicodeContent(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	plaintext := "Hello, \u4e16\u754c! \U0001F600"

	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt unicode failed: %v", err)
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt unicode failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted unicode doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	plaintext := "same-content"

	// Encrypt the same content multiple times
	ciphertext1, _ := enc.Encrypt(plaintext)
	ciphertext2, _ := enc.Encrypt(plaintext)
	ciphertext3, _ := enc.Encrypt(plaintext)

	// All ciphertexts should be different (due to random nonce)
	if string(ciphertext1) == string(ciphertext2) {
		t.Fatal("Ciphertexts 1 and 2 are identical - nonce not random")
	}
	if string(ciphertext2) == string(ciphertext3) {
		t.Fatal("Ciphertexts 2 and 3 are identical - nonce not random")
	}

	// But all should decrypt to the same plaintext
	d1, _ := enc.Decrypt(ciphertext1)
	d2, _ := enc.Decrypt(ciphertext2)
	d3, _ := enc.Decrypt(ciphertext3)

	if d1 != plaintext || d2 != plaintext || d3 != plaintext {
		t.Fatal("Decryption of different ciphertexts failed")
	}
}

func TestDecrypt_InvalidCiphertext(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Try to decrypt garbage
	_, err = enc.Decrypt([]byte("invalid ciphertext"))
	if err == nil {
		t.Fatal("Expected error when decrypting invalid ciphertext")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Ciphertext too short for nonce
	_, err = enc.Decrypt([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("Expected error when decrypting too-short ciphertext")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc1, _ := NewEncryptor("key-one")
	enc2, _ := NewEncryptor("key-two")

	plaintext := "secret data"
	ciphertext, _ := enc1.Encrypt(plaintext)

	// Try to decrypt with different key
	_, err := enc2.Decrypt(ciphertext)
	if err == nil {
		t.Fatal("Expected error when decrypting with wrong key")
	}
}

func TestEncryptToBase64_DecryptFromBase64(t *testing.T) {
	enc, err := NewEncryptor("test-key")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	plaintext := "my-oauth-refresh-token"

	// Encrypt to base64
	encoded, err := enc.EncryptToBase64(plaintext)
	if err != nil {
		t.Fatalf("EncryptToBase64 failed: %v", err)
	}

	// Verify it's valid base64
	_, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("Output is not valid base64: %v", err)
	}

	// Decrypt from base64
	decrypted, err := enc.DecryptFromBase64(encoded)
	if err != nil {
		t.Fatalf("DecryptFromBase64 failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted text doesn't match: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptFromBase64_InvalidBase64(t *testing.T) {
	enc, _ := NewEncryptor("test-key")

	_, err := enc.DecryptFromBase64("not-valid-base64!!!")
	if err == nil {
		t.Fatal("Expected error when decrypting invalid base64")
	}
}
