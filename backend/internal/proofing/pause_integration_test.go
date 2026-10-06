//go:build integration

package proofing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestPauseStore(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewPauseStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	audited := func(action string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = $1 AND organization_id = $2`,
			action, orgID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if p, err := store.Get(ctx, orgID); err != nil || p.Paused() || p.OrganizationID != orgID {
		t.Fatalf("never paused = %+v, %v; want active", p, err)
	}
	p, err := store.Set(ctx, orgID, PausePlatform, PauseOn, nil)
	if err != nil || p.PlatformPausedAt == nil || p.OrgPausedAt != nil {
		t.Fatalf("platform pause = %+v, %v", p, err)
	}
	since := *p.PlatformPausedAt
	if again, err := store.Set(ctx, orgID, PausePlatform, PauseOn, nil); err != nil || !again.PlatformPausedAt.Equal(since) {
		t.Errorf("pausing again = %+v, %v; want the first pause kept", again, err)
	}
	if n := audited(audit.IdentityProofingPaused); n != 1 {
		t.Errorf("paused audited %d times, want 1 (a repeat changes nothing)", n)
	}
	if p, err = store.Set(ctx, orgID, PauseOrganization, PauseOn, nil); err != nil || p.PlatformPausedAt == nil || p.OrgPausedAt == nil {
		t.Fatalf("org pause beside platform = %+v, %v; want both", p, err)
	}
	if list, err := store.List(ctx); err != nil || len(list) != 1 || list[0].OrganizationID != orgID {
		t.Errorf("List = %+v, %v; want the org", list, err)
	}
	if p, err = store.Set(ctx, orgID, PausePlatform, PauseOff, nil); err != nil || !p.Paused() || p.PlatformPausedAt != nil {
		t.Errorf("platform resume with the org's switch off = %+v, %v; want still paused by the org", p, err)
	}
	if p, err = store.Set(ctx, orgID, PauseOrganization, PauseOff, nil); err != nil || p.Paused() {
		t.Errorf("both lifted = %+v, %v; want active", p, err)
	}
	if n := audited(audit.IdentityProofingResumed); n != 2 {
		t.Errorf("resumed audited %d times, want 2", n)
	}
	if list, err := store.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("List after resuming = %+v, %v; want none", list, err)
	}
	if _, err := store.Set(ctx, uuid.New(), PausePlatform, PauseOn, nil); !errors.Is(err, ErrOrgNotFound) {
		t.Errorf("unknown org = %v, want ErrOrgNotFound", err)
	}
}

// A pause keeps who set it while it holds: a second admin pausing again does
// not take it over, and lifting it forgets them.
func TestPauseKeepsWhoPaused(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewPauseStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	kim := makeUser(t, pool, "kim@example.org")
	ctx := context.Background()
	if _, err := store.Set(ctx, orgID, PausePlatform, PauseOn, &sam); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p, err := store.Set(ctx, orgID, PausePlatform, PauseOn, &kim)
	if err != nil || p.PlatformPausedBy == nil || p.PlatformPausedBy.UserID != sam || p.PlatformPausedBy.Name == "" {
		t.Fatalf("paused by = %+v, %v; want sam, with a name", p.PlatformPausedBy, err)
	}
	if p, err = store.Set(ctx, orgID, PausePlatform, PauseOff, &kim); err != nil || p.PlatformPausedBy != nil {
		t.Errorf("after resume paused by = %+v, %v; want none", p.PlatformPausedBy, err)
	}
}
