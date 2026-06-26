package auth

import (
	"net/http"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	forbiddenCode = "forbidden"
	forbiddenMsg  = "forbidden"
)

type PlatformAdmins map[string]struct{}

func NewPlatformAdmins(emails []string) PlatformAdmins {
	set := make(PlatformAdmins, len(emails))
	for _, e := range emails {
		set[normalizeEmail(e)] = struct{}{}
	}
	return set
}

func (p PlatformAdmins) Has(email string) bool {
	_, ok := p[normalizeEmail(email)]
	return ok
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
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
