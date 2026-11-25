package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	unauthenticatedCode = "unauthenticated"
	unauthenticatedMsg  = "unauthenticated"
	internalErrorCode   = "internal_error"
	internalErrorMsg    = "internal server error"
)

type sessionLookuper interface {
	Lookup(ctx context.Context, rawToken string) (user.User, error)
}

// RequireUser gates a handler behind a valid session cookie. It is applied
// per-route in Register, not globally, so the public /auth/* routes stay open.
func RequireUser(sessions sessionLookuper) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := readSessionCookie(r)
			if !ok {
				respond.Error(w, r, http.StatusUnauthorized, unauthenticatedCode, unauthenticatedMsg)
				return
			}

			// Expired and unknown/forged tokens both return ErrInvalidSession, so
			// there's no branch on expiry and no session-existence leak.
			u, err := sessions.Lookup(r.Context(), raw)
			if errors.Is(err, session.ErrInvalidSession) {
				respond.Error(w, r, http.StatusUnauthorized, unauthenticatedCode, unauthenticatedMsg)
				return
			}
			// A failed lookup says nothing about the session: a 401 here would
			// sign the user out of the app over a database hiccup.
			if err != nil {
				if respond.ClientGone(r) {
					return
				}
				slog.ErrorContext(r.Context(), "looking up session", slog.String("error", err.Error()))
				respond.Error(w, r, http.StatusInternalServerError, internalErrorCode, internalErrorMsg)
				return
			}

			ctx := ContextWithUser(r.Context(), u)
			ctx = audit.ContextWithActor(ctx, audit.Actor{UserID: u.ID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
