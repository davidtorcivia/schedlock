// Package crypto provides cryptographic utilities for the application.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

// Encryptor handles AES-256-GCM encryption for sensitive data like OAuth tokens.
type Encryptor struct {
	key []byte // 32 bytes for AES-256
}

// NewEncryptor creates a new Encryptor from a base64-encoded key or derives one from a secret.
func NewEncryptor(keyOrSecret string) (*Encryptor, error) {
	// Try to decode as base64 first (32 bytes = 256 bits)
	key, err := base64.StdEncoding.DecodeString(keyOrSecret)
	if err == nil && len(key) == 32 {
		return &Encryptor{key: key}, nil
	}

	// If not valid base64 or wrong length, derive key using HKDF
	key, err = deriveKey([]byte(keyOrSecret), nil, "schedlock-encryption")
	if err != nil {
		return nil, fmt.Errorf("failed to derive encryption key: %w", err)
	}

	return &Encryptor{key: key}, nil
}

// deriveKey uses HKDF-SHA256 to derive a 32-byte key from input material.
func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	if salt == nil {
		salt = make([]byte, 32)
	}

	hkdfReader := hkdf.New(sha256.New, secret, salt, []byte(info))
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, err
	}

	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns base64-encoded ciphertext (nonce prepended).
func (e *Encryptor) Encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and prepend nonce
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// Decrypt decrypts AES-256-GCM ciphertext.
// Expects nonce to be prepended to ciphertext.
func (e *Encryptor) Decrypt(ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and decrypt
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptToBase64 encrypts and returns base64-encoded result.
func (e *Encryptor) EncryptToBase64(plaintext string) (string, error) {
	ciphertext, err := e.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptFromBase64 decodes base64 and decrypts.
func (e *Encryptor) DecryptFromBase64(encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	return e.Decrypt(ciphertext)
}
