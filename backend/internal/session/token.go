package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

func newToken() (raw string, hash [sha256.Size]byte, err error) {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b)
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashToken(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}
