package proofing

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestCursor is where a page of a customer's requests ends: the last one's
// creation and id, newest first.
type RequestCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Page sizes of the customer API's session list.
const (
	DefaultPageSize = 50
	MaxPageSize     = 100
)

func encodeRequestCursor(c RequestCursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeRequestCursor reads an encodeRequestCursor value; false for anything else.
func decodeRequestCursor(s string) (RequestCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return RequestCursor{}, false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return RequestCursor{}, false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return RequestCursor{}, false
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return RequestCursor{}, false
	}
	return RequestCursor{CreatedAt: createdAt, ID: parsed}, true
}
