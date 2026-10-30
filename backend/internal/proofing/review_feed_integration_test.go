//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// reviewFeedWait bounds how long a NOTIFY takes to reach the feed.
const reviewFeedWait = 5 * time.Second

// An address decision reaches the feed of its org through the trigger, and not
// the feed of another.
func TestReviewFeedHearsAnAddressDecision(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	otherID := makeOrg(t, pool, "beta")
	sam := makeUser(t, pool, "sam@example.org")
	pending := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")

	feed := NewReviewFeed()
	changed, stop := feed.subscribe(orgID)
	defer stop()
	otherChanged, stopOther := feed.subscribe(otherID)
	defer stopOther()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go feed.Run(ctx, pool)
	// Every subscriber is told once LISTEN holds.
	waitTold(t, changed, "LISTEN holding")
	waitTold(t, otherChanged, "LISTEN holding")

	if at, err := requests.DecideAddressReview(ctx, pending, makeUser(t, pool, "rita@example.org"), AddressReviewConfirmed, "Matches"); err != nil || at == nil {
		t.Fatalf("DecideAddressReview = %v, %v; want decided", at, err)
	}
	waitTold(t, changed, "the address decision")
	if told(otherChanged) {
		t.Error("another org's feed was told of the decision")
	}
}

func waitTold(t *testing.T, changed <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(reviewFeedWait):
		t.Fatalf("not told of %s within %s", what, reviewFeedWait)
	}
}
