package attestation_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// fakeOfferQueue is an in-memory stand-in for the credential-offer queue. It
// implements both halves the split gives us — the receive side (RecordOffer) and
// the decide side (list / accept / decline) — so one fake backs a test that
// follows an offer from delivery to decision.
type fakeOfferQueue struct {
	mu        sync.Mutex
	offers    []attestation.CredentialOffer
	held      []attestation.HeldInput
	recordErr error
}

func newFakeOfferQueue() *fakeOfferQueue { return &fakeOfferQueue{} }

func (q *fakeOfferQueue) RecordOffer(_ context.Context, orgID uuid.UUID, in attestation.OfferInput) (attestation.CredentialOffer, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.recordErr != nil {
		return attestation.CredentialOffer{}, false, q.recordErr
	}
	// Mirror the real store's unique (organization_id, source_message_id): a
	// re-delivered message resolves to the row already queued, whatever it decided.
	for _, o := range q.offers {
		if o.OrganizationID == orgID && o.SourceMessageID == in.SourceMessageID {
			return o, false, nil
		}
	}
	o := attestation.CredentialOffer{
		ID:              uuid.New(),
		OrganizationID:  orgID,
		SourceMessageID: in.SourceMessageID,
		SenderOrgName:   in.SenderOrgName,
		SenderAddress:   in.SenderAddress,
		FromParty:       in.FromParty,
		CredentialName:  in.CredentialName,
		Offer:           in.Offer,
		Status:          attestation.OfferPending,
	}
	q.offers = append(q.offers, o)
	return o, true, nil
}

func (q *fakeOfferQueue) ListPendingOffers(_ context.Context, orgID uuid.UUID) ([]attestation.CredentialOffer, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := []attestation.CredentialOffer{}
	for _, o := range q.offers {
		if o.OrganizationID == orgID && o.Status == attestation.OfferPending {
			pending = append(pending, o)
		}
	}
	return pending, nil
}

func (q *fakeOfferQueue) GetPendingOffer(_ context.Context, orgID, id uuid.UUID) (attestation.CredentialOffer, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, o := range q.offers {
		if o.ID == id && o.OrganizationID == orgID && o.Status == attestation.OfferPending {
			return o, nil
		}
	}
	return attestation.CredentialOffer{}, attestation.ErrOfferNotFound
}

func (q *fakeOfferQueue) AcceptOffer(_ context.Context, orgID, id uuid.UUID, in attestation.HeldInput) (attestation.HeldAttestation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, o := range q.offers {
		if o.ID == id && o.OrganizationID == orgID && o.Status == attestation.OfferPending {
			q.offers[i].Status = attestation.OfferAccepted
			q.held = append(q.held, in)
			return attestation.HeldAttestation{ID: uuid.New(), OrganizationID: orgID, VCT: in.VCT}, nil
		}
	}
	return attestation.HeldAttestation{}, attestation.ErrOfferNotFound
}

func (q *fakeOfferQueue) DeclineOffer(_ context.Context, orgID, id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, o := range q.offers {
		if o.ID == id && o.OrganizationID == orgID && o.Status == attestation.OfferPending {
			q.offers[i].Status = attestation.OfferDeclined
			return nil
		}
	}
	return attestation.ErrOfferNotFound
}

func (q *fakeOfferQueue) queued() []attestation.CredentialOffer {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]attestation.CredentialOffer(nil), q.offers...)
}

func (q *fakeOfferQueue) heldCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.held)
}

// inbound builds the notification qerds.Service hands the receiver. Both sender
// identities are explicit: fromParty is what the gateway verified, sender is what
// the sending side claimed.
func inbound(orgID, msgID uuid.UUID, fromParty, sender, body string) qerds.Inbound {
	return qerds.Inbound{
		OrgID:     orgID,
		MessageID: msgID,
		FromParty: fromParty,
		Sender:    sender,
		Subject:   "subject",
		Body:      body,
	}
}

func offerBody(t *testing.T) string {
	t.Helper()
	body, err := attestation.MarshalCredentialOfferEnvelope("Acme", "Registration", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	return body
}

// Receiving an offer must not put a credential in the wallet: it queues the offer
// for the organization to accept or decline (issue #229).
func TestOfferReceiverQueuesWithoutLoading(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))

	orgID, msgID := uuid.New(), uuid.New()
	if err := rec.OnInboundMessage(context.Background(), inbound(orgID, msgID, "blue_gw", "acme@qerds.localhost", offerBody(t))); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}

	queued := queue.queued()
	if len(queued) != 1 {
		t.Fatalf("expected 1 queued offer, got %d", len(queued))
	}
	got := queued[0]
	if got.Status != attestation.OfferPending {
		t.Errorf("queued offer status = %q, want %q", got.Status, attestation.OfferPending)
	}
	if got.Offer != "openid-credential-offer://?x=1" {
		t.Errorf("queued offer deeplink = %q", got.Offer)
	}
	if got.SourceMessageID != msgID {
		t.Errorf("SourceMessageID = %v, want %v", got.SourceMessageID, msgID)
	}
	if got.SenderOrgName != "Acme" || got.CredentialName != "Registration" {
		t.Errorf("envelope metadata not carried through: %+v", got)
	}
	if got.SenderAddress != "acme@qerds.localhost" || got.FromParty != "blue_gw" {
		t.Errorf("sender identities not carried through: %+v", got)
	}
	if queue.heldCount() != 0 {
		t.Errorf("receiving an offer loaded %d credentials into the wallet; it must wait for acceptance", queue.heldCount())
	}
}

func TestOfferReceiverIgnoresNonOffer(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))

	if err := rec.OnInboundMessage(context.Background(), inbound(uuid.New(), uuid.New(), "blue_gw", "acme@qerds.localhost", "just a human message")); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(queue.queued()) != 0 {
		t.Errorf("non-offer message must not queue an offer (%d queued)", len(queue.queued()))
	}
}

// A re-delivered message must not queue a second offer, and — the case that
// matters once a human is in the loop — must not reopen a decision already made.
func TestOfferReceiverIdempotentOnRedelivery(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))

	ctx := context.Background()
	orgID, msgID := uuid.New(), uuid.New()
	body := offerBody(t)
	if err := rec.OnInboundMessage(ctx, inbound(orgID, msgID, "blue_gw", "acme@qerds.localhost", body)); err != nil {
		t.Fatalf("first OnInboundMessage: %v", err)
	}
	if err := queue.DeclineOffer(ctx, orgID, queue.queued()[0].ID); err != nil {
		t.Fatalf("decline: %v", err)
	}

	if err := rec.OnInboundMessage(ctx, inbound(orgID, msgID, "blue_gw", "acme@qerds.localhost", body)); err != nil {
		t.Fatalf("second OnInboundMessage: %v", err)
	}
	queued := queue.queued()
	if len(queued) != 1 {
		t.Fatalf("re-delivery queued the offer again (%d queued)", len(queued))
	}
	if queued[0].Status != attestation.OfferDeclined {
		t.Errorf("re-delivery reopened a declined offer (status %q)", queued[0].Status)
	}
}

func TestOfferReceiverReturnsQueueError(t *testing.T) {
	queue := newFakeOfferQueue()
	queue.recordErr = errors.New("database down")
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))

	err := rec.OnInboundMessage(context.Background(), inbound(uuid.New(), uuid.New(), "blue_gw", "acme@qerds.localhost", offerBody(t)))
	if err == nil {
		t.Fatal("expected an error when the queue write fails")
	}
}
