//go:build integration

package attestation_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

// inboundMessage inserts a received QERDS message for the org, returning its id —
// credential_offers references it, so the queue cannot be exercised without one.
func inboundMessage(t *testing.T, ctx context.Context, e env, providerRef string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	const insert = `INSERT INTO qerds_messages
		(organization_id, direction, sender_address, recipient_address, subject, body, provider_ref, status)
		VALUES ($1, 'inbound', 'verid@ver.id', 'acme@qerds.localhost', 'Credential offer', '', $2, 'received')
		RETURNING id`
	if err := e.pool.QueryRow(ctx, insert, e.orgID, providerRef).Scan(&id); err != nil {
		t.Fatalf("insert inbound message: %v", err)
	}
	return id
}

func offerInput(messageID uuid.UUID) attestation.OfferInput {
	return attestation.OfferInput{
		SourceMessageID: messageID,
		SenderOrgName:   "Ver.ID",
		SenderAddress:   "verid@ver.id",
		FromParty:       "verid-qerds",
		CredentialName:  "Bewijs van inschrijving",
		Offer:           "openid-credential-offer://?credential_offer=%7B%22x%22%3A1%7D",
	}
}

func TestRecordOfferQueuesOnceAndListsPending(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-queue")

	offer, recorded, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}
	if !recorded {
		t.Fatal("the first delivery of an offer must be recorded")
	}
	if offer.Status != attestation.OfferPending {
		t.Errorf("status = %q, want %q", offer.Status, attestation.OfferPending)
	}
	if offer.DecidedAt != nil {
		t.Errorf("a pending offer has no decision timestamp, got %v", offer.DecidedAt)
	}

	// Re-delivery of the same message resolves to the row already queued.
	again, recorded, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer on re-delivery: %v", err)
	}
	if recorded {
		t.Error("a re-delivered offer must not be queued a second time")
	}
	if again.ID != offer.ID {
		t.Errorf("re-delivery returned offer %s, want the queued %s", again.ID, offer.ID)
	}

	pending, err := e.store.ListPendingOffers(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListPendingOffers: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending offers = %d, want 1", len(pending))
	}
	if pending[0].Offer != offerInput(messageID).Offer {
		t.Errorf("the offer deeplink did not round-trip: %q", pending[0].Offer)
	}
	if pending[0].SenderOrgName != "Ver.ID" || pending[0].CredentialName != "Bewijs van inschrijving" {
		t.Errorf("envelope metadata did not round-trip: %+v", pending[0])
	}
}

func TestAcceptOfferRecordsHeldAndClosesTheOffer(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-accept")

	offer, _, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}

	claimed, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID)
	if err != nil {
		t.Fatalf("ClaimOffer: %v", err)
	}
	if claimed.Status != attestation.OfferAccepting {
		t.Errorf("claimed status = %q, want %q", claimed.Status, attestation.OfferAccepting)
	}
	if claimed.Offer != offerInput(messageID).Offer {
		t.Errorf("the claim did not return the deeplink to redeem: %q", claimed.Offer)
	}
	if claimed.DecidedAt != nil {
		t.Errorf("a claimed offer is not settled yet, got decidedAt %v", claimed.DecidedAt)
	}
	// A claimed offer is out of the queue, so a second admin has nothing to accept.
	pendingWhileClaimed, err := e.store.ListPendingOffers(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListPendingOffers: %v", err)
	}
	if len(pendingWhileClaimed) != 0 {
		t.Fatalf("a claimed offer is still listed as pending (%d)", len(pendingWhileClaimed))
	}

	held, err := e.store.AcceptOffer(ctx, e.orgID, offer.ID, attestation.HeldInput{
		CredentialRef:   "ref-1",
		VCT:             "nl.kvk.registration",
		Issuer:          "https://issuer.ver.id",
		Source:          attestation.HeldSourceQERDS,
		SourceMessageID: &messageID,
	})
	if err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	if held.VCT != "nl.kvk.registration" || held.Source != attestation.HeldSourceQERDS {
		t.Errorf("held row mismatch: %+v", held)
	}
	if held.SourceMessageID == nil || *held.SourceMessageID != messageID {
		t.Errorf("held row lost the QERDS evidence link: %v", held.SourceMessageID)
	}

	pending, err := e.store.ListPendingOffers(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListPendingOffers: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("accepted offer still pending (%d)", len(pending))
	}

	// The status guard is what makes a double accept safe: an accepted offer can
	// be neither re-claimed nor settled again.
	if _, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("ClaimOffer on an accepted offer = %v, want ErrOfferNotFound", err)
	}
	if _, err := e.store.AcceptOffer(ctx, e.orgID, offer.ID, attestation.HeldInput{
		CredentialRef: "ref-2", VCT: "nl.kvk.registration", Issuer: "https://issuer.ver.id",
		Source: attestation.HeldSourceQERDS, SourceMessageID: &messageID,
	}); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("second AcceptOffer = %v, want ErrOfferNotFound", err)
	}
	all, err := e.store.ListHeld(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListHeld: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("held credentials = %d, want 1 — the offer was accepted twice", len(all))
	}
}

func TestDeclineOfferHoldsNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-decline")

	offer, _, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}
	if err := e.store.DeclineOffer(ctx, e.orgID, offer.ID); err != nil {
		t.Fatalf("DeclineOffer: %v", err)
	}

	held, err := e.store.ListHeld(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListHeld: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("declining recorded %d held credentials", len(held))
	}
	if _, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("ClaimOffer after decline = %v, want ErrOfferNotFound", err)
	}
	if err := e.store.DeclineOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("second DeclineOffer = %v, want ErrOfferNotFound", err)
	}

	// A re-delivered message must not resurrect a decision the org already made.
	_, recorded, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer on re-delivery: %v", err)
	}
	if recorded {
		t.Error("re-delivery re-queued an offer the organization declined")
	}
}

// An offer belongs to one organization: another tenant must not see or decide it.
func TestOfferQueueIsOrgScoped(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-scope")

	offer, _, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}

	other := uuid.New()
	if _, err := e.store.ClaimOffer(ctx, other, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("ClaimOffer for another org = %v, want ErrOfferNotFound", err)
	}
	if err := e.store.DeclineOffer(ctx, other, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("DeclineOffer for another org = %v, want ErrOfferNotFound", err)
	}
	pending, err := e.store.ListPendingOffers(ctx, other)
	if err != nil {
		t.Fatalf("ListPendingOffers: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("another org sees %d of this org's offers", len(pending))
	}
}

// TestClaimOfferIsTakenOnce is the SQL half of the guard the service relies on:
// however many accepts race for one offer, Postgres hands the claim to exactly
// one of them. The service calls the issuer on the strength of that, so a claim
// two callers could both win would put two credentials in the org's engine and
// index only one.
func TestClaimOfferIsTakenOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-claim-race")

	offer, _, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}

	const claimers = 8
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		mu    sync.Mutex
		won   int
		errs  []error
	)
	start.Add(1)
	for range claimers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			_, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			won++
		}()
	}
	start.Done()
	done.Wait()

	if won != 1 {
		t.Fatalf("%d of %d concurrent claims succeeded, want exactly 1", won, claimers)
	}
	for _, err := range errs {
		if !errors.Is(err, attestation.ErrOfferNotFound) {
			t.Fatalf("losing claim = %v, want ErrOfferNotFound", err)
		}
	}
}

// Releasing is what keeps a failed redemption retriable: the offer goes back in
// the queue, and only from a claim — a settled decision cannot be reopened.
func TestReleaseOfferReturnsTheOfferToTheQueue(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	messageID := inboundMessage(t, ctx, e, "ref-release")

	offer, _, err := e.store.RecordOffer(ctx, e.orgID, offerInput(messageID))
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}
	// Nothing to release while the offer is still waiting for a decision.
	if err := e.store.ReleaseOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("ReleaseOffer on a pending offer = %v, want ErrOfferNotFound", err)
	}

	if _, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID); err != nil {
		t.Fatalf("ClaimOffer: %v", err)
	}
	if err := e.store.ReleaseOffer(ctx, e.orgID, offer.ID); err != nil {
		t.Fatalf("ReleaseOffer: %v", err)
	}

	pending, err := e.store.ListPendingOffers(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListPendingOffers: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending offers after release = %d, want 1", len(pending))
	}
	if pending[0].ID != offer.ID {
		t.Fatalf("released offer %s, want %s", pending[0].ID, offer.ID)
	}

	// Released means claimable again, and settling still works from that claim.
	if _, err := e.store.ClaimOffer(ctx, e.orgID, offer.ID); err != nil {
		t.Fatalf("ClaimOffer after release: %v", err)
	}
	if _, err := e.store.AcceptOffer(ctx, e.orgID, offer.ID, attestation.HeldInput{
		CredentialRef: "ref-retry", VCT: "nl.kvk.registration", Issuer: "https://issuer.ver.id",
		Source: attestation.HeldSourceQERDS, SourceMessageID: &messageID,
	}); err != nil {
		t.Fatalf("AcceptOffer after release: %v", err)
	}
	if err := e.store.ReleaseOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("ReleaseOffer on an accepted offer = %v, want ErrOfferNotFound", err)
	}
}
