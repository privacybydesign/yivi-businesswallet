//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// The overview counts and lists by effective status, as the per-status lists
// do: a lapsed link is finished (expired), not waiting.
func TestRequestOverview(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	running := time.Now().Add(SessionTTL)

	pending := createdRequest(t, store, hostedRequestIn(t, orgID, sam, running))
	lapsed := createdRequest(t, store, hostedRequestIn(t, orgID, sam, time.Now().Add(-time.Minute)))
	outcomes := map[Status]Request{}
	for _, status := range []Status{StatusNeedsReview, StatusApproved, StatusRejected} {
		sessionID := "s-" + string(status)
		req := startedUntil(t, store, orgID, sam, sessionID, running)
		if err := store.RecordOutcome(ctx, req, sessionID, status, proofingprovider.Result{}); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		outcomes[status] = req
	}

	o, err := store.Overview(ctx, orgID, OverviewScope{RequestedBy: &sam, Held: true})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if o.Unfinished != 1 || o.Held != 1 {
		t.Errorf("counts = %d unfinished, %d held; want 1, 1", o.Unfinished, o.Held)
	}
	assertIDs(t, "waiting", o.Waiting, outcomes[StatusNeedsReview].ID, pending.ID)
	assertIDs(t, "finished", o.Finished, outcomes[StatusRejected].ID, outcomes[StatusApproved].ID, lapsed.ID)

	o, err = store.Overview(ctx, orgID, OverviewScope{})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if o.Held != 0 {
		t.Errorf("held = %d for a reader who decides, want 0", o.Held)
	}
	assertIDs(t, "waiting", o.Waiting, pending.ID)

	other := uuid.New()
	o, err = store.Overview(ctx, orgID, OverviewScope{RequestedBy: &other, Held: true})
	if err != nil || o.Unfinished != 0 || len(o.Waiting) != 0 || len(o.Finished) != 0 {
		t.Errorf("another sender's overview = %+v, %v; want empty", o, err)
	}
}

// The overview shows each list's first overviewRows and counts them all.
func TestRequestOverviewCutsRows(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	running := time.Now().Add(SessionTTL)
	first := createdRequest(t, store, hostedRequestIn(t, orgID, sam, running))
	for range overviewRows {
		createdRequest(t, store, hostedRequestIn(t, orgID, sam, running))
	}

	o, err := store.Overview(context.Background(), orgID, OverviewScope{})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if o.Unfinished != overviewRows+1 || len(o.Waiting) != overviewRows || o.Waiting[0].ID != first.ID {
		t.Errorf("overview = %d unfinished, %d rows; want %d, %d, the longest waiting first",
			o.Unfinished, len(o.Waiting), overviewRows+1, overviewRows)
	}
}

func assertIDs(t *testing.T, list string, got []Request, want ...uuid.UUID) {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if len(ids) != len(want) {
		t.Fatalf("%s = %v, want %v", list, ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("%s = %v, want %v", list, ids, want)
		}
	}
}
