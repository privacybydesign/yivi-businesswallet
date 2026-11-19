package openid4vppresenter

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const idBytes = 32

// newID mints the random, client-facing transaction id and its hash. The raw id
// goes to the browser (and rides through the login redirect as the only state);
// only the hash is persisted — mirrors internal/presentation.
func newID() (raw string, hash [sha256.Size]byte, err error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("openid4vppresenter: mint id: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashID(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}
