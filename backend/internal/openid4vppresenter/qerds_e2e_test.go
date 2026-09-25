package openid4vppresenter

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// TestPresentationRequestOverQERDSEndToEnd exercises the receive half of
// "credential disclosure between orgs over QERDS" (issue #271) through the real
// qerds.Service: org A sends the invocation as a QERDS message, and org B's
// inbound consumer validates it and queues it org-bound at StatusOrgSelected —
// exactly where a browser-driven org selection (#112) leaves a transaction for
// the governance layer to decide (#113). Receiving the request must not, by
// itself, disclose anything; deciding it is out of this seam's scope.
func TestPresentationRequestOverQERDSEndToEnd(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	requesterOrg, holderOrg := uuid.New(), uuid.New()
	const holderAddress = "holder@qerds.localhost"
	requesterQ := newMemQerdsForPresenter(requesterOrg, "requester@qerds.localhost")
	holderQ := newMemQerdsForPresenter(holderOrg, holderAddress)
	svcRequester := qerds.NewService(requesterQ, requesterQ, prov)
	svcHolder := qerds.NewService(holderQ, holderQ, prov)

	presenterSvc, store, _ := newTestService()
	svcHolder.SetInboundConsumer(NewReceiver(presenterSvc))

	body, err := MarshalPresentationRequestEnvelope("Requester Org", testClientID, "https://v/req", "")
	if err != nil {
		t.Fatalf("marshal presentation request: %v", err)
	}
	if _, err := svcRequester.Send(ctx, requesterOrg, "", holderAddress, "Presentation request", body, nil); err != nil {
		t.Fatalf("requester send: %v", err)
	}

	n, err := svcHolder.Poll(ctx, holderOrg)
	if err != nil {
		t.Fatalf("holder poll: %v", err)
	}
	if n != 1 {
		t.Fatalf("holder received %d messages, want 1", n)
	}

	queued := store.forOrg(holderOrg)
	if len(queued) != 1 {
		t.Fatalf("expected 1 queued presentation request, got %d", len(queued))
	}
	if queued[0].Status != StatusOrgSelected {
		t.Errorf("status = %q, want %q", queued[0].Status, StatusOrgSelected)
	}
	if queued[0].VerifierIdentity == "" {
		t.Error("queued request carries no verifier identity")
	}
}

// memQerdsForPresenter is a minimal in-memory qerds messageStore + addressStore
// for a single org, enough to drive qerds.Service without a database. Mirrors
// attestation_test's memQerds (offer_qerds_e2e_test.go); duplicated rather than
// shared because it backs an unexported test seam in each package.
type memQerdsForPresenter struct {
	orgID   uuid.UUID
	address string

	mu     sync.Mutex
	stored map[string]qerds.Message
}

func newMemQerdsForPresenter(orgID uuid.UUID, address string) *memQerdsForPresenter {
	return &memQerdsForPresenter{orgID: orgID, address: address, stored: map[string]qerds.Message{}}
}

func (m *memQerdsForPresenter) CreateOutbound(_ context.Context, orgID uuid.UUID, sender, recipient, subject, body string, _ []qerdsprovider.Attachment) (qerds.Message, error) {
	return qerds.Message{
		ID: uuid.New(), OrganizationID: orgID, Direction: qerds.DirectionOutbound,
		SenderAddress: sender, RecipientAddress: recipient, Subject: subject, Body: body, Status: qerds.StatusSubmitted,
	}, nil
}

func (m *memQerdsForPresenter) RecordSent(_ context.Context, _ uuid.UUID, _ qerdsprovider.SendReceipt) error {
	return nil
}

func (m *memQerdsForPresenter) CreateInbound(_ context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (qerds.Message, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.stored[in.ProviderRef]; ok {
		return existing, false, nil
	}
	msg := qerds.Message{
		ID: uuid.New(), OrganizationID: orgID, Direction: qerds.DirectionInbound,
		SenderAddress: string(in.Sender), RecipientAddress: string(in.Recipient),
		Subject: in.Subject, Body: in.Body, ProviderRef: in.ProviderRef, Status: qerds.StatusReceived,
	}
	m.stored[in.ProviderRef] = msg
	return msg, true, nil
}

func (m *memQerdsForPresenter) DefaultAddress(_ context.Context, _ uuid.UUID) (qerds.Address, error) {
	return qerds.Address{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}, nil
}

func (m *memQerdsForPresenter) ListAddresses(_ context.Context, _ uuid.UUID) ([]qerds.Address, error) {
	return []qerds.Address{{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}}, nil
}

func (m *memQerdsForPresenter) AllAddresses(_ context.Context) ([]qerds.Address, error) {
	return []qerds.Address{{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}}, nil
}

func (m *memQerdsForPresenter) OrgByAddress(_ context.Context, address string) (uuid.UUID, error) {
	if address == m.address {
		return m.orgID, nil
	}
	return uuid.Nil, qerds.ErrAddressNotFound
}
