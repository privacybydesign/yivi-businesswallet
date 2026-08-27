package attestation_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// These tests exercise the full "OpenID4VCI credential offer over QERDS" path in
// process — a Yivi org issues an org-subject credential to another org, the offer
// travels as a QERDS message, the receiving org's inbound consumer queues it, an
// admin accepts, and only then is the credential redeemed into the holder engine,
// indexed as held and displayable. They wire the real qerds.Service +
// attestation.OfferReceiver + attestation.Service + eudiholder.StubHolder, with
// in-memory qerds stores, so no database or live issuer is needed.

// memQerds is an in-memory qerds messageStore + addressStore for a single org.
// CreateInbound dedupes on provider ref and, on a repeat, returns the *existing*
// message (not a zero value) so the service can re-run the idempotent consumer on
// a re-delivery — the contract the real store must honour (see message_store.go).
type memQerds struct {
	orgID   uuid.UUID
	address string

	mu     sync.Mutex
	stored map[string]qerds.Message // inbound, keyed by provider ref
}

func newMemQerds(orgID uuid.UUID, address string) *memQerds {
	return &memQerds{orgID: orgID, address: address, stored: map[string]qerds.Message{}}
}

func (m *memQerds) CreateOutbound(_ context.Context, orgID uuid.UUID, sender, recipient, subject, body string, _ []qerdsprovider.Attachment) (qerds.Message, error) {
	return qerds.Message{
		ID:               uuid.New(),
		OrganizationID:   orgID,
		Direction:        qerds.DirectionOutbound,
		SenderAddress:    sender,
		RecipientAddress: recipient,
		Subject:          subject,
		Body:             body,
		Status:           qerds.StatusSubmitted,
	}, nil
}

func (m *memQerds) RecordSent(_ context.Context, _ uuid.UUID, _ qerdsprovider.SendReceipt) error {
	return nil
}

func (m *memQerds) CreateInbound(_ context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (qerds.Message, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.stored[in.ProviderRef]; ok {
		return existing, false, nil
	}
	msg := qerds.Message{
		ID:               uuid.New(),
		OrganizationID:   orgID,
		Direction:        qerds.DirectionInbound,
		SenderAddress:    string(in.Sender),
		RecipientAddress: string(in.Recipient),
		Subject:          in.Subject,
		Body:             in.Body,
		ProviderRef:      in.ProviderRef,
		Status:           qerds.StatusReceived,
	}
	m.stored[in.ProviderRef] = msg
	return msg, true, nil
}

func (m *memQerds) DefaultAddress(_ context.Context, _ uuid.UUID) (qerds.Address, error) {
	return qerds.Address{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}, nil
}

func (m *memQerds) ListAddresses(_ context.Context, _ uuid.UUID) ([]qerds.Address, error) {
	return []qerds.Address{{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}}, nil
}

func (m *memQerds) AllAddresses(_ context.Context) ([]qerds.Address, error) {
	return []qerds.Address{{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}}, nil
}

func (m *memQerds) OrgByAddress(_ context.Context, address string) (uuid.UUID, error) {
	if address == m.address {
		return m.orgID, nil
	}
	return uuid.Nil, qerds.ErrAddressNotFound
}

// flakyHolder is a holder engine whose first failuresLeft redemptions fail,
// simulating a transient issuer/verification error at the moment of acceptance.
// It embeds the stub so the rest of the engine seam behaves normally.
type flakyHolder struct {
	*eudiholder.StubHolder
	failuresLeft int
	calls        int
}

func (h *flakyHolder) Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (eudiholder.Redeemed, error) {
	h.calls++
	if h.failuresLeft > 0 {
		h.failuresLeft--
		return eudiholder.Redeemed{}, errors.New("issuer temporarily unavailable")
	}
	return h.StubHolder.Redeem(ctx, orgID, offerURI)
}

const (
	yiviAddress = "yivi@qerds.localhost"
	ruAddress   = "ru@qerds.localhost"
)

// TestCredentialOfferOverQERDSEndToEnd is the happy path: Yivi issues an offer to
// the RU org over QERDS, RU polls and the offer lands in its decision queue
// WITHOUT touching the wallet, an admin accepts, and only then does RU hold the
// credential and its attributes display.
func TestCredentialOfferOverQERDSEndToEnd(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	yiviOrg, ruOrg := uuid.New(), uuid.New()
	yiviQ := newMemQerds(yiviOrg, yiviAddress)
	ruQ := newMemQerds(ruOrg, ruAddress)
	svcYivi := qerds.NewService(yiviQ, yiviQ, prov)
	svcRU := qerds.NewService(ruQ, ruQ, prov)

	holder := eudiholder.NewStubHolder()
	queue := newFakeOfferQueue()
	svcRU.SetInboundConsumer(attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil)))

	body, err := attestation.MarshalCredentialOfferEnvelope("Yivi", "Approved supplier", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	if _, err := svcYivi.Send(ctx, yiviOrg, "", ruAddress, "Credential offer: Approved supplier", body, nil); err != nil {
		t.Fatalf("Yivi send: %v", err)
	}

	n, err := svcRU.Poll(ctx, ruOrg)
	if err != nil {
		t.Fatalf("RU poll: %v", err)
	}
	if n != 1 {
		t.Fatalf("RU received %d messages, want 1", n)
	}

	// The whole point of the acceptance step: receiving the offer holds nothing.
	if queue.heldCount() != 0 {
		t.Fatalf("receiving the offer loaded %d credentials into the wallet before anyone accepted", queue.heldCount())
	}
	// The console side: only the offer queue and the holder engine are exercised
	// by the accept path, so the rest of the service's collaborators stay nil.
	service := attestation.NewService(nil, nil, nil, nil, nil, nil, queue, holder, "http://app.test")
	pending, err := service.ListOffers(ctx, ruOrg)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 offer awaiting a decision, got %d — the offer was swallowed", len(pending))
	}
	if pending[0].SenderOrgName != "Yivi" || pending[0].CredentialName != "Approved supplier" {
		t.Errorf("the offer shown for acceptance does not name what was offered: %+v", pending[0])
	}

	// An admin accepts: now the credential is redeemed and indexed as held.
	if _, err := service.AcceptOffer(ctx, ruOrg, pending[0].ID); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	if queue.heldCount() != 1 {
		t.Fatalf("expected 1 held credential after acceptance, got %d", queue.heldCount())
	}
	rec := queue.held[0]
	if rec.Source != attestation.HeldSourceQERDS {
		t.Errorf("held source = %q, want %q", rec.Source, attestation.HeldSourceQERDS)
	}
	if rec.CredentialRef == "" {
		t.Error("held credential has no engine ref")
	}
	if rec.SourceMessageID == nil || *rec.SourceMessageID != pending[0].SourceMessageID {
		t.Errorf("held row lost the QERDS evidence link: %v", rec.SourceMessageID)
	}

	// Display side: the held credential's attributes resolve from the holder engine.
	cred, err := holder.Claims(ctx, ruOrg, rec.CredentialRef, rec.VCT, "en")
	if err != nil {
		t.Fatalf("claims: %v", err)
	}
	if len(cred.Attributes) == 0 {
		t.Fatal("received credential displays no attributes")
	}
}

// A re-delivered offer must not queue a second decision — the org would be asked
// twice about one credential, and accepting both would hold it twice.
func TestCredentialOfferQueuedOnceOnRedelivery(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	ruOrg := uuid.New()
	ruQ := newMemQerds(ruOrg, ruAddress)
	svcRU := qerds.NewService(ruQ, ruQ, prov)

	queue := newFakeOfferQueue()
	svcRU.SetInboundConsumer(attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil)))

	body, err := attestation.MarshalCredentialOfferEnvelope("Yivi", "Approved supplier", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	in := qerdsprovider.InboundMessage{
		ProviderRef: "provider-ref-fixed",
		Sender:      yiviAddress,
		Recipient:   ruAddress,
		Subject:     "Credential offer: Approved supplier",
		Body:        body,
	}

	for i := range 2 {
		if err := svcRU.ReceiveInbound(ctx, in); err != nil {
			t.Fatalf("ReceiveInbound %d: %v", i+1, err)
		}
	}
	if len(queue.queued()) != 1 {
		t.Fatalf("re-delivery queued the offer twice (%d queued)", len(queue.queued()))
	}
}

// An offer whose redemption fails at the moment of acceptance must stay pending,
// so the admin can accept again once the issuer is back or the org's wallet is
// activated — the offer is never spent on a failed attempt.
func TestAcceptOfferRetriableAfterRedeemFailure(t *testing.T) {
	ctx := context.Background()
	ruOrg := uuid.New()

	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))
	body, err := attestation.MarshalCredentialOfferEnvelope("Yivi", "Approved supplier", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	if err := rec.OnInboundMessage(ctx, inbound(ruOrg, uuid.New(), "blue_gw", yiviAddress, body)); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	offerID := queue.queued()[0].ID

	holder := &flakyHolder{StubHolder: eudiholder.NewStubHolder(), failuresLeft: 1}
	service := attestation.NewService(nil, nil, nil, nil, nil, nil, queue, holder, "http://app.test")

	if _, err := service.AcceptOffer(ctx, ruOrg, offerID); err == nil {
		t.Fatal("expected the first accept to fail while the issuer is unavailable")
	}
	if queue.heldCount() != 0 {
		t.Fatalf("a failed redemption recorded %d held credentials", queue.heldCount())
	}
	if queue.queued()[0].Status != attestation.OfferPending {
		t.Fatalf("a failed accept left the offer %q; it must stay pending to be retried", queue.queued()[0].Status)
	}

	if _, err := service.AcceptOffer(ctx, ruOrg, offerID); err != nil {
		t.Fatalf("second accept: %v", err)
	}
	if queue.heldCount() != 1 {
		t.Fatalf("expected 1 held credential after the retry, got %d", queue.heldCount())
	}
	if holder.calls != 2 {
		t.Fatalf("expected 2 redeem attempts (fail, then succeed), got %d", holder.calls)
	}

	// The offer is spent: accepting the same one again finds nothing pending.
	if _, err := service.AcceptOffer(ctx, ruOrg, offerID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("re-accepting a decided offer = %v, want ErrOfferNotFound", err)
	}
}

// Declining is the other half of the acceptance requirement: the credential must
// never reach the wallet, and the decision must be final.
func TestDeclineOfferNeverLoadsTheCredential(t *testing.T) {
	ctx := context.Background()
	ruOrg := uuid.New()

	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue, attestation.NewTrustedOfferSenders(nil, nil))
	body, err := attestation.MarshalCredentialOfferEnvelope("Yivi", "Approved supplier", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	if err := rec.OnInboundMessage(ctx, inbound(ruOrg, uuid.New(), "blue_gw", yiviAddress, body)); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	offerID := queue.queued()[0].ID

	holder := &flakyHolder{StubHolder: eudiholder.NewStubHolder()}
	service := attestation.NewService(nil, nil, nil, nil, nil, nil, queue, holder, "http://app.test")

	if err := service.DeclineOffer(ctx, ruOrg, offerID); err != nil {
		t.Fatalf("decline offer: %v", err)
	}
	if queue.heldCount() != 0 {
		t.Fatalf("declining loaded %d credentials into the wallet", queue.heldCount())
	}
	if holder.calls != 0 {
		t.Fatalf("declining redeemed the offer (%d calls)", holder.calls)
	}
	pending, err := service.ListOffers(ctx, ruOrg)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a declined offer is still awaiting a decision (%d pending)", len(pending))
	}
	if _, err := service.AcceptOffer(ctx, ruOrg, offerID); !errors.Is(err, attestation.ErrOfferNotFound) {
		t.Fatalf("accepting a declined offer = %v, want ErrOfferNotFound", err)
	}
}
