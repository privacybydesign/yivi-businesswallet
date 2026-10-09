package proofing

import (
	"encoding/base32"
	"strings"

	"github.com/google/uuid"
)

// sessionIDPrefix starts a request's id on the customer API and in webhooks:
// an opaque ps_ id, never the database UUID, so its format can change later.
const sessionIDPrefix = "ps_"

var publicIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// publicSessionID is a request's customer-facing id.
func publicSessionID(id uuid.UUID) string {
	return sessionIDPrefix + strings.ToLower(publicIDEncoding.EncodeToString(id[:]))
}

// parsePublicSessionID reads a publicSessionID back; false for anything else.
func parsePublicSessionID(s string) (uuid.UUID, bool) {
	raw, ok := strings.CutPrefix(s, sessionIDPrefix)
	if !ok {
		return uuid.Nil, false
	}
	b, err := publicIDEncoding.DecodeString(strings.ToUpper(raw))
	if err != nil || len(b) != len(uuid.UUID{}) {
		return uuid.Nil, false
	}
	return uuid.UUID(b), true
}
