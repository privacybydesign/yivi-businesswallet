//go:build integration

package attestation_test

import (
	"context"
	"errors"
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

	// The status guard is what makes a double accept safe.
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
	if _, err := e.store.GetPendingOffer(ctx, e.orgID, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("GetPendingOffer after decline = %v, want ErrOfferNotFound", err)
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
	if _, err := e.store.GetPendingOffer(ctx, other, offer.ID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("GetPendingOffer for another org = %v, want ErrOfferNotFound", err)
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
