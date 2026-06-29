package auth

import (
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	forbiddenCode = "forbidden"
	forbiddenMsg  = "forbidden"
)

type PlatformAdmins map[user.Email]struct{}

func NewPlatformAdmins(emails []string) PlatformAdmins {
	set := make(PlatformAdmins, len(emails))
	for _, e := range emails {
		email, err := user.ParseEmail(e)
		if err != nil {
			continue
		}
		set[email] = struct{}{}
	}
	return set
}

func (p PlatformAdmins) Has(email user.Email) bool {
	_, ok := p[email]
	return ok
}

// RequirePlatformAdmin gates a handler behind platform-admin status. It must be
// composed inside RequireUser, which puts the user in context.
func RequirePlatformAdmin(admins PlatformAdmins) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if !admins.Has(u.Email) {
				respond.Error(w, r, http.StatusForbidden, forbiddenCode, forbiddenMsg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
