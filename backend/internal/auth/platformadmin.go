package auth

import (
	"net/http"
	"sync"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	forbiddenCode = "forbidden"
	forbiddenMsg  = "forbidden"
)

// PlatformAdmins is the set of platform administrators: PLATFORM_ADMIN_EMAILS,
// plus a dev stack's bootstrap admin (DevBootstrap) once claimed. Copies share
// the set; the zero value has none.
type PlatformAdmins struct {
	set *platformAdminSet
}

type platformAdminSet struct {
	mu     sync.RWMutex
	emails map[user.Email]struct{}
}

func NewPlatformAdmins(emails []string) PlatformAdmins {
	set := &platformAdminSet{emails: make(map[user.Email]struct{}, len(emails))}
	for _, e := range emails {
		email, err := user.ParseEmail(e)
		if err != nil {
			continue
		}
		set.emails[email] = struct{}{}
	}
	return PlatformAdmins{set: set}
}

func (p PlatformAdmins) Has(email user.Email) bool {
	if p.set == nil {
		return false
	}
	p.set.mu.RLock()
	defer p.set.mu.RUnlock()
	_, ok := p.set.emails[email]
	return ok
}

// add makes email a platform admin for every holder of p.
func (p PlatformAdmins) add(email user.Email) {
	p.set.mu.Lock()
	defer p.set.mu.Unlock()
	p.set.emails[email] = struct{}{}
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
