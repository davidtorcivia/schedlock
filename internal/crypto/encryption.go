// Package crypto provides authenticated encryption for data at rest.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// ErrCiphertextTooShort is returned when input cannot contain a nonce.
var ErrCiphertextTooShort = errors.New("ciphertext too short")

// Encryptor performs AES-256-GCM encryption for OAuth tokens and provider
// credentials. The AEAD is built once and is safe for concurrent use.
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor creates an Encryptor from a base64-encoded 32-byte key, or
// derives a key via HKDF-SHA256 when the input is an arbitrary secret.
func NewEncryptor(keyOrSecret string) (*Encryptor, error) {
	if keyOrSecret == "" {
		return nil, errors.New("encryption key is required")
	}

	key, err := base64.StdEncoding.DecodeString(keyOrSecret)
	if err != nil || len(key) != 32 {
		key, err = deriveKey([]byte(keyOrSecret), "schedlock-encryption")
		if err != nil {
			return nil, fmt.Errorf("failed to derive encryption key: %w", err)
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	return &Encryptor{aead: aead}, nil
}

// deriveKey stretches arbitrary input into a 32-byte key with HKDF-SHA256.
func deriveKey(secret []byte, info string) ([]byte, error) {
	// A fixed (zero) salt keeps derivation deterministic across restarts, which
	// is required to decrypt data written by a previous run. HKDF remains sound
	// with a constant salt as long as the input keying material is secret.
	salt := make([]byte, sha256.Size)

	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, []byte(info)), key); err != nil {
		return nil, err
	}
	return key, nil
}

// Encrypt seals plaintext, returning nonce||ciphertext.
func (e *Encryptor) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return e.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens a nonce||ciphertext value produced by Encrypt.
func (e *Encryptor) Decrypt(ciphertext []byte) (string, error) {
	nonceSize := e.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", ErrCiphertextTooShort
	}

	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := e.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptToBase64 seals plaintext and base64-encodes the result.
func (e *Encryptor) EncryptToBase64(plaintext string) (string, error) {
	ciphertext, err := e.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptFromBase64 decodes and opens a base64-encoded ciphertext.
func (e *Encryptor) DecryptFromBase64(encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	return e.Decrypt(ciphertext)
}
