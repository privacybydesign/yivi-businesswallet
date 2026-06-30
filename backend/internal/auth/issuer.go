package auth

import (
	"context"
	"crypto/sha256"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
)

// SessionIssuer mints a session and sets the cookie for a user verified outside
// the login flow — namely after an invitation accept, whose identity disclosure
// already proves email ownership. It lets the organization handler log a member
// in on accept without reaching into session/cookie internals.
type SessionIssuer struct {
	sessions *session.Store
	cookie   CookieConfig
}

func NewSessionIssuer(sessions *session.Store, cookie CookieConfig) *SessionIssuer {
	return &SessionIssuer{sessions: sessions, cookie: cookie}
}

func (i *SessionIssuer) Issue(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, idempotencyToken string) error {
	raw, err := i.sessions.Mint(ctx, userID, sha256.Sum256([]byte(idempotencyToken)))
	if err != nil {
		return err
	}
	setSessionCookie(w, raw, i.cookie)
	return nil
}
