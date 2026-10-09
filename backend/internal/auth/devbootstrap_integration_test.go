//go:build integration

package auth

import (
	"context"
	"sync"
	"testing"

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
