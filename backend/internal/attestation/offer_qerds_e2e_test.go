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
// travels as a QERDS message, the receiving org's inbound consumer redeems it into
// its holder engine and indexes it as held, and the held credential's attributes
// then display. They wire the real qerds.Service + attestation.OfferReceiver +
// eudiholder.StubHolder, with in-memory qerds stores, so no database or live
// issuer is needed.

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

func (m *memQerds) OrgByAddress(_ context.Context, address string) (uuid.UUID, error) {
	if address == m.address {
		return m.orgID, nil
	}
	return uuid.Nil, qerds.ErrAddressNotFound
}

// memHeld is an in-memory held recorder implementing attestation's heldRecorder
// seam: HeldForMessage is the idempotency guard, RecordHeld stores the index row.
type memHeld struct {
	mu      sync.Mutex
	records []attestation.HeldInput
	byMsg   map[uuid.UUID]bool
}

func newMemHeld() *memHeld {
	return &memHeld{byMsg: map[uuid.UUID]bool{}}
}

func (h *memHeld) HeldForMessage(_ context.Context, _ uuid.UUID, messageID uuid.UUID) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byMsg[messageID], nil
}

func (h *memHeld) RecordHeld(_ context.Context, _ uuid.UUID, in attestation.HeldInput) (attestation.HeldAttestation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, in)
	if in.SourceMessageID != nil {
		h.byMsg[*in.SourceMessageID] = true
	}
	return attestation.HeldAttestation{}, nil
}

func (h *memHeld) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

// flakyRedeemer wraps a holder engine and fails the first failures redeems,
// simulating a transient issuer/verification error on receipt.
type flakyRedeemer struct {
	inner        *eudiholder.StubHolder
	failuresLeft int
	calls        int
}

func (r *flakyRedeemer) Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (eudiholder.Redeemed, error) {
	r.calls++
	if r.failuresLeft > 0 {
		r.failuresLeft--
		return eudiholder.Redeemed{}, errors.New("issuer temporarily unavailable")
	}
	return r.inner.Redeem(ctx, orgID, offerURI)
}

const (
	yiviAddress = "yivi@qerds.localhost"
	ruAddress   = "ru@qerds.localhost"
)

// TestCredentialOfferOverQERDSEndToEnd is the happy path: Yivi issues an offer to
// the RU org over QERDS, RU polls, redeems, holds the credential, and its
// attributes display.
func TestCredentialOfferOverQERDSEndToEnd(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	yiviOrg, ruOrg := uuid.New(), uuid.New()
	yiviQ := newMemQerds(yiviOrg, yiviAddress)
	ruQ := newMemQerds(ruOrg, ruAddress)
	svcYivi := qerds.NewService(yiviQ, yiviQ, prov)
	svcRU := qerds.NewService(ruQ, ruQ, prov)

	holder := eudiholder.NewStubHolder()
	held := newMemHeld()
	svcRU.SetInboundConsumer(attestation.NewOfferReceiver(holder, held))

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

	if held.count() != 1 {
		t.Fatalf("expected 1 held credential after receiving the offer, got %d — the offer was swallowed", held.count())
	}
	rec := held.records[0]
	if rec.Source != attestation.HeldSourceQERDS {
		t.Errorf("held source = %q, want %q", rec.Source, attestation.HeldSourceQERDS)
	}
	if rec.CredentialRef == "" {
		t.Error("held credential has no engine ref")
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

// TestCredentialOfferRetriedOnRedelivery reproduces the swallow: a received offer
// whose first redeem fails must be retried when the same message is re-delivered.
// The inbound consumer is idempotent, so re-delivery re-attempts the redemption
// rather than being deduped away forever.
func TestCredentialOfferRetriedOnRedelivery(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	ruOrg := uuid.New()
	ruQ := newMemQerds(ruOrg, ruAddress)
	svcRU := qerds.NewService(ruQ, ruQ, prov)

	holder := eudiholder.NewStubHolder()
	held := newMemHeld()
	redeemer := &flakyRedeemer{inner: holder, failuresLeft: 1}
	svcRU.SetInboundConsumer(attestation.NewOfferReceiver(redeemer, held))

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

	// First delivery: the redeem fails and the consumer error is swallowed, so
	// nothing is held yet.
	if err := svcRU.ReceiveInbound(ctx, in); err != nil {
		t.Fatalf("first ReceiveInbound: %v", err)
	}
	if redeemer.calls != 1 {
		t.Fatalf("expected 1 redeem attempt on first delivery, got %d", redeemer.calls)
	}
	if held.count() != 0 {
		t.Fatalf("expected nothing held after a failed redeem, got %d", held.count())
	}

	// Re-delivery of the same message must re-run the consumer and retry the
	// redeem — otherwise the received offer is swallowed forever.
	if err := svcRU.ReceiveInbound(ctx, in); err != nil {
		t.Fatalf("second ReceiveInbound: %v", err)
	}
	if redeemer.calls != 2 {
		t.Fatalf("re-delivery did not retry the redeem (attempts=%d): a received offer whose first redeem failed is swallowed forever", redeemer.calls)
	}
	if held.count() != 1 {
		t.Fatalf("expected the credential to be held after the retry, got %d", held.count())
	}
}
