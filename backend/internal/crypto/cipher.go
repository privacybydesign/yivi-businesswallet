// Package crypto provides a small AES-256-GCM helper for encrypting secrets at
// rest under a deployment-level key (e.g. per-org SMTP passwords). Ciphertext is
// stored as nonce || sealed, with a fresh random nonce per encryption.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// keyBytes is the AES-256 key length. The configured key is hex-encoded 32 bytes
// (64 hex chars), e.g. `openssl rand -hex 32`.
const keyBytes = 32

// Cipher wraps AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a hex-encoded 32-byte key. An empty key returns
// (nil, nil) so callers can treat the feature as not-configured; a present but
// malformed key is a hard error (a real misconfiguration).
func NewCipher(hexKey string) (*Cipher, error) {
	if hexKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: encryption key must be hex-encoded: %w", err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("crypto: encryption key must be %d bytes (got %d)", keyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: init gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext, returning nonce || ciphertext.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, expecting nonce || ciphertext.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("crypto: ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}
