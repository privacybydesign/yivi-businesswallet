//go:build integration

package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// Of first logins at once, exactly one becomes the dev stack's admin; a later
// unknown address does not, and a restart keeps the admin.
func TestDevBootstrapClaimsOnce(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	users := user.NewStore(pool)
	admins := NewPlatformAdmins(nil)
	b := NewDevBootstrap(pool, users, admins)

	emails := []user.Email{"first@dev.test", "second@dev.test", "third@dev.test"}
	claimed := make([]bool, len(emails))
	var wg sync.WaitGroup
	for i, email := range emails {
		wg.Go(func() {
			_, ok, err := b.claim(ctx, email)
			if err != nil {
				t.Errorf("claim %s: %v", email, err)
			}
			claimed[i] = ok
		})
	}
	wg.Wait()

	winners := 0
	var winner user.Email
	for i, ok := range claimed {
		if ok {
			winners++
			winner = emails[i]
		}
	}
	if winners != 1 || !admins.Has(winner) {
		t.Fatalf("claimed %v, want exactly one platform admin", claimed)
	}
	for _, email := range emails {
		if _, err := users.FindByEmail(ctx, email); (err == nil) != (email == winner) {
			t.Errorf("user %s exists = %v, want only the winner created", email, err == nil)
		}
	}
	if _, ok, err := b.claim(ctx, "later@dev.test"); err != nil || ok {
		t.Errorf("a later claim = %v, %v; want refused", ok, err)
	}

	restarted := NewPlatformAdmins(nil)
	if err := NewDevBootstrap(pool, users, restarted).Load(ctx); err != nil || !restarted.Has(winner) {
		t.Errorf("after a restart Has(%s) = %v, %v; want the admin kept", winner, restarted.Has(winner), err)
	}
}

// invitedOnly reports pending invitations for one address.
type invitedOnly struct{ email user.Email }

func (i invitedOnly) PendingInvitationsForEmail(_ context.Context, email user.Email) ([]PendingInvite, error) {
	if email != i.email {
		return nil, nil
	}
	return []PendingInvite{{ID: uuid.New(), OrganizationName: "Acme", OrganizationSlug: "acme"}}, nil
}

// Through the login path: an invited address is told about its invitations,
// never made admin; without a bootstrap (DEV_MODE off) an unknown address is
// refused; with one, the first unknown address becomes the admin.
func TestUnknownLoginAndTheBootstrap(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	users := user.NewStore(pool)
	admins := NewPlatformAdmins(nil)
	invited := invitedOnly{email: "invited@dev.test"}

	off := &Service{invites: invited}
	if _, err := off.unknownUser(ctx, "stranger@dev.test"); !errors.Is(err, errUserNotInvited) {
		t.Errorf("unknown login without a bootstrap = %v, want errUserNotInvited", err)
	}

	on := &Service{invites: invited, bootstrap: NewDevBootstrap(pool, users, admins)}
	var pending *PendingInvitesError
	if _, err := on.unknownUser(ctx, "invited@dev.test"); !errors.As(err, &pending) || admins.Has("invited@dev.test") {
		t.Errorf("invited login = %v, admin %v; want its invitations, not the admin", err, admins.Has("invited@dev.test"))
	}
	if u, err := on.unknownUser(ctx, "first@dev.test"); err != nil || u.Email != "first@dev.test" || !admins.Has("first@dev.test") {
		t.Errorf("first unknown login = %+v, %v; want it made the admin", u, err)
	}
	if _, err := on.unknownUser(ctx, "second@dev.test"); !errors.Is(err, errUserNotInvited) {
		t.Errorf("a later unknown login = %v, want errUserNotInvited", err)
	}
}
