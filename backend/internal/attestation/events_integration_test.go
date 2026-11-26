//go:build integration

package attestation_test

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/events"
)

// eventWait bounds how long a NOTIFY takes to reach the hub.
const eventWait = 5 * time.Second

// A held credential that arrives or changes status tells the org's attestation
// pages, so the holder sees a revocation without the QERDS notice.
func TestEventsHearAHeldStatusChange(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := events.NewHub()
	changed, stop := hub.Subscribe(events.Topic(events.Attestations, e.orgID))
	defer stop()
	go hub.Run(ctx, e.pool)
	// Every subscriber is told once LISTEN holds.
	waitAttestationsEvent(t, changed, "LISTEN holding")

	held, err := e.store.RecordHeld(ctx, e.orgID, attestation.HeldInput{
		CredentialRef: "ref-1", VCT: "uri:eu.eudi.eucc.1", Issuer: "https://issuer.example", Source: attestation.HeldSourceOpenID4VCI,
	})
	if err != nil {
		t.Fatalf("RecordHeld: %v", err)
	}
	waitAttestationsEvent(t, changed, "the held credential arriving")

	recorded, err := e.store.RecordHeldStatusChange(ctx, e.orgID, held.ID, held.VCT, attestation.HeldRevoked)
	if err != nil || !recorded {
		t.Fatalf("RecordHeldStatusChange = %v, %v; want recorded", recorded, err)
	}
	waitAttestationsEvent(t, changed, "the held credential's revocation")
}

func waitAttestationsEvent(t *testing.T, changed <-chan events.Kind, what string) {
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
