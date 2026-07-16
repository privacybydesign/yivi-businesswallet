package presentation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const idBytes = 32

// newID mints a random client-facing session id and its hash. The raw id goes to
// the client; only the hash is persisted (mirrors internal/session).
func newID() (raw string, hash [sha256.Size]byte, err error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashID(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}
