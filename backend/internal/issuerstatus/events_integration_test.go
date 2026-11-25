//go:build integration

package issuerstatus

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/events"
)

// eventWait bounds how long a NOTIFY takes to reach the hub.
const eventWait = 5 * time.Second

// The issuer reserving an entry is when the issuing org's offer is claimed, so it
// tells that org's attestation pages at once.
func TestTakeTellsTheIssuingOrg(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	issuedID := e.seedOffer(t, "state-live", "offered")
	var orgID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT organization_id FROM issued_attestations WHERE id = $1`, issuedID).Scan(&orgID); err != nil {
		t.Fatalf("org of offer: %v", err)
	}

	hub := events.NewHub()
	changed, stop := hub.Subscribe(events.Topic(events.Attestations, orgID))
	defer stop()
	go hub.Run(ctx, e.pool)
	// Every subscriber is told once LISTEN holds.
	waitEvent(t, changed, "LISTEN holding")

	if resp := e.take(t, testAPIKey, takeForm("state-live")); resp.StatusCode != http.StatusOK {
		t.Fatalf("take = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	waitEvent(t, changed, "the reserved entry")
}

func waitEvent(t *testing.T, changed <-chan events.Kind, what string) {
	t.Helper()
	select {
	case kind := <-changed:
		if kind != events.Attestations {
			t.Fatalf("heard %q for %s, want %q", kind, what, events.Attestations)
		}
	case <-time.After(eventWait):
		t.Fatalf("not told of %s within %s", what, eventWait)
	}
}
