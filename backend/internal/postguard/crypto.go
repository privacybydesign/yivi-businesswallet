package postguard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// keyBytes is the AES-256 key length. The configured master key-encryption key
// is a hex-encoded 32-byte value (64 hex chars), e.g. `openssl rand -hex 32`.
const keyBytes = 32

// deriveDEK turns an owner-supplied secret of any length into a 32-byte data
// encryption key. The DEK's secrecy rests on the master key that wraps it at
// rest (envelope encryption), so a plain SHA-256 mapping is sufficient here.
func deriveDEK(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Cipher wraps AES-256-GCM for encrypting an org's API key at rest. Ciphertext
// is stored as nonce || sealed, so a fresh random nonce is used per encryption.
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
		return nil, fmt.Errorf("postguard: key-encryption key must be hex-encoded: %w", err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("postguard: key-encryption key must be %d bytes (got %d)", keyBytes, len(key))
	}
	return newCipherFromKey(key)
}

// newCipherFromKey builds a Cipher from raw 32-byte key material (a per-org DEK).
func newCipherFromKey(key []byte) (*Cipher, error) {
	if len(key) != keyBytes {
		return nil, fmt.Errorf("postguard: key must be %d bytes (got %d)", keyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("postguard: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("postguard: init gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext, returning nonce || ciphertext.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("postguard: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, expecting nonce || ciphertext.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("postguard: ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("postguard: decrypt: %w", err)
	}
	return plaintext, nil
}
