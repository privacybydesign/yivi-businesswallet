//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/events"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// eventWait bounds how long a NOTIFY takes to reach the hub.
const eventWait = 5 * time.Second

// An address decision reaches the event stream of its org through the
// triggers, and not the stream of another; its request's own followers hear
// the request move.
func TestEventsHearAnAddressDecision(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	otherID := makeOrg(t, pool, "beta")
	sam := makeUser(t, pool, "sam@example.org")
	pending := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")

	hub := events.NewHub()
	changed, stop := hub.Subscribe(events.Topic(events.Reviews, orgID))
	defer stop()
	otherChanged, stopOther := hub.Subscribe(events.Topic(events.Reviews, otherID))
	defer stopOther()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx, pool)
	// Every subscriber is told once LISTEN holds.
	waitEvent(t, changed, events.Reviews, "LISTEN holding")
	waitEvent(t, otherChanged, events.Reviews, "LISTEN holding")

	if at, err := requests.DecideStepReview(ctx, pending, ReviewKindAddress, makeUser(t, pool, "rita@example.org"), AddressReviewConfirmed, "Matches"); err != nil || at == nil {
		t.Fatalf("DecideAddressReview = %v, %v; want decided", at, err)
	}
	waitEvent(t, changed, events.Reviews, "the address decision")
	select {
	case <-otherChanged:
		t.Error("another org's stream was told of the decision")
	default:
	}

	// A request's own row moving tells who follows that request and the org's
	// verify pages.
	request, stopRequest := hub.Subscribe(events.Topic(events.ProofingRequest, pending.ID), events.Topic(events.ProofingProgress, orgID))
	defer stopRequest()
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET updated_at = now() WHERE id = $1`, pending.ID); err != nil {
		t.Fatalf("touch request: %v", err)
	}
	got := map[events.Kind]bool{}
	for len(got) < 2 {
		select {
		case kind := <-request:
			got[kind] = true
		case <-time.After(eventWait):
			t.Fatalf("heard %v of the request moving, want its own and the org's progress event", got)
		}
	}
}

func waitEvent(t *testing.T, changed <-chan events.Kind, want events.Kind, what string) {
	t.Helper()
	select {
	case kind := <-changed:
		if kind != want {
			t.Fatalf("heard %q for %s, want %q", kind, what, want)
		}
	case <-time.After(eventWait):
		t.Fatalf("not told of %s within %s", what, eventWait)
	}
}

// The one list of everything that waits holds exactly what the four lists the
// pages used to merge hold between them.
func TestWaitingReviewsListIsTheFourListsInOne(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")
	// It reads needs_review as well, so two of the four lists hold it.
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET status = 'needs_review', completed_at = now()
		WHERE id = (SELECT id FROM identity_proofing_requests WHERE organization_id = $1 ORDER BY created_at LIMIT 1)`, orgID); err != nil {
		t.Fatalf("put a request in review: %v", err)
	}

	needsReview := StatusNeedsReview
	want := map[string]bool{}
	filters := []RequestFilter{{Status: &needsReview}}
	for _, kind := range []ReviewKind{ReviewKindAddress, ReviewKindEmployment, ReviewKindDiplomas} {
		filters = append(filters, RequestFilter{ReviewKind: &kind})
	}
	for _, filter := range filters {
		listed, err := requests.List(ctx, orgID, filter)
		if err != nil {
			t.Fatalf("List %+v: %v", filter, err)
		}
		for _, req := range listed {
			want[req.ID.String()] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("the fixture has nothing waiting")
	}

	waiting, err := requests.List(ctx, orgID, RequestFilter{WaitingReviews: true})
	if err != nil {
		t.Fatalf("List waiting: %v", err)
	}
	got := map[string]bool{}
	for _, req := range waiting {
		got[req.ID.String()] = true
	}
	if len(got) != len(waiting) {
		t.Errorf("the waiting list repeats a request: %d rows, %d distinct", len(waiting), len(got))
	}
	if len(got) != len(want) {
		t.Fatalf("waiting list has %d requests, the four lists %d", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("request %s is in one of the four lists but not in the waiting list", id)
		}
	}
}
