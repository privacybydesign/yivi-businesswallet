package openid4vprequester_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vppresenter"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/relyingparty"
)

const (
	requesterAddress = "acme@qerds.localhost"
	holderAddress    = "globex@qerds.localhost"
	requestedVCT     = "nl.kvk.registration"
	e2eTTL           = time.Hour
)

// TestOrgToOrgCredentialRequestEndToEnd drives #271 both ways without a
// database: org A signs a request under a certificate from its deployment's
// requester CA and sends it over the real qerds.Service; org B's receiver
// fetches the Request Object over HTTP from A's public endpoint, verifies it
// with the production validator against that CA's root, binds it to the QERDS
// sender and queues it under A's certified name; B's admin approves, and A's
// response endpoint accepts the answer and records the disclosure.
func TestOrgToOrgCredentialRequestEndToEnd(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()
	requesterOrg := organization.Organization{ID: uuid.New(), Name: "Acme B.V."}
	holderOrg := uuid.New()
	qerdsA := qerds.NewService(newMemQerds(requesterOrg.ID, requesterAddress), newMemQerds(requesterOrg.ID, requesterAddress), prov)
	holderQ := newMemQerds(holderOrg, holderAddress)
	qerdsB := qerds.NewService(holderQ, holderQ, prov)

	ca, err := relyingparty.NewCA("Test Requester CA")
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	mux := http.NewServeMux()
	passThrough := func(next http.Handler) http.Handler { return next }
	srv := httptest.NewServer(http.StripPrefix("/api/v1", mux))
	t.Cleanup(srv.Close)
	requester := openid4vprequester.NewService(store, qerdsA, ca, verifiedAs(requestedVCT), srv.URL, e2eTTL)
	openid4vprequester.NewHandler(requester, nil, passThrough, passThrough).Register(mux)

	// B: the production validator, trusting A's CA; plain http because the
	// test server is on loopback.
	trust, err := eudiholder.NewVerifierTrust(ca.RootPEM(), false)
	if err != nil {
		t.Fatal(err)
	}
	policy := openid4vppresenter.Policy{AllowInsecureHTTP: true}
	presenterStore := newMemPresenterStore()
	presenter := openid4vppresenter.NewService(presenterStore, nil, eudiholder.NewStubHolder(),
		openid4vppresenter.NewFetcher(policy), openid4vppresenter.NewVerifyingValidator(trust, policy),
		openid4vppresenter.NewResponder(policy), false)
	qerdsB.SetInboundConsumer(openid4vppresenter.NewReceiver(presenter))

	sent, err := requester.Send(ctx, requesterOrg, uuid.New(), openid4vprequester.SendInput{
		Recipient:   holderAddress,
		Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT, Claims: []string{"legalName"}}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.QerdsMessageID == nil || sent.SenderAddress != requesterAddress {
		t.Fatalf("sent request = %+v, want it linked to a QERDS message from %s", sent, requesterAddress)
	}

	if _, err := qerdsB.Poll(ctx, holderOrg); err != nil {
		t.Fatalf("holder poll: %v", err)
	}
	pending, err := presenter.PendingApprovals(ctx, holderOrg)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("holder queue has %d requests, want 1", len(pending))
	}
	if pending[0].VerifierIdentity != requesterOrg.Name {
		t.Errorf("queued requester = %q, want the certified %q", pending[0].VerifierIdentity, requesterOrg.Name)
	}
	if got := store.get(sent.ID); got.RequestFetchedAt == nil {
		t.Error("the Request Object was not marked fetched")
	}

	if _, err := presenter.Approve(ctx, holderOrg, pending[0].ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	done := store.get(sent.ID)
	if done.Status != openid4vprequester.StatusCompleted {
		t.Fatalf("request status = %q (reason %v), want completed", done.Status, done.FailureReason)
	}
	if len(done.Disclosed) != 1 || done.Disclosed[0].VCT != requestedVCT || done.Disclosed[0].Claims["legalName"] != "Globex" {
		t.Errorf("disclosed = %+v", done.Disclosed)
	}
}

// verifiedAs stands in for the issuer-signature and key-binding check, which
// the stub holder's placeholder token cannot pass: every presentation verifies
// as a credential of type vct. relyingparty's own tests cover the real check.
type verifiedAs string

func (v verifiedAs) Verify(token map[string][]string, _, _ string) []relyingparty.Presented {
	var out []relyingparty.Presented
	for queryID := range token {
		out = append(out, relyingparty.Presented{
			QueryID: queryID, VCT: string(v), Issuer: "https://issuer.test", Verified: true,
			Claims: map[string]any{"legalName": "Globex"},
		})
	}
	return out
}

// memQerds is an in-memory qerds message + address store for one organization,
// the same shape as openid4vppresenter's memQerdsForPresenter.
type memQerds struct {
	orgID   uuid.UUID
	address string

	mu     sync.Mutex
	stored map[string]qerds.Message
}

func newMemQerds(orgID uuid.UUID, address string) *memQerds {
	return &memQerds{orgID: orgID, address: address, stored: map[string]qerds.Message{}}
}

func (m *memQerds) CreateOutbound(_ context.Context, orgID uuid.UUID, sender, recipient, subject, body string, _ []qerdsprovider.Attachment) (qerds.Message, error) {
	return qerds.Message{
		ID: uuid.New(), OrganizationID: orgID, Direction: qerds.DirectionOutbound,
		SenderAddress: sender, RecipientAddress: recipient, Subject: subject, Body: body, Status: qerds.StatusSubmitted,
	}, nil
}

func (m *memQerds) RecordSent(context.Context, uuid.UUID, qerdsprovider.SendReceipt) error {
	return nil
}

func (m *memQerds) CreateInbound(_ context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (qerds.Message, bool, error) {
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

func (m *memQerds) addr() qerds.Address {
	return qerds.Address{ID: uuid.New(), OrganizationID: m.orgID, Address: m.address, IsDefault: true}
}

func (m *memQerds) DefaultAddress(context.Context, uuid.UUID) (qerds.Address, error) {
	return m.addr(), nil
}

func (m *memQerds) ListAddresses(context.Context, uuid.UUID) ([]qerds.Address, error) {
	return []qerds.Address{m.addr()}, nil
}

func (m *memQerds) AllAddresses(context.Context) ([]qerds.Address, error) {
	return []qerds.Address{m.addr()}, nil
}

func (m *memQerds) OrgByAddress(_ context.Context, address string) (uuid.UUID, error) {
	if address == m.address {
		return m.orgID, nil
	}
	return uuid.Nil, qerds.ErrAddressNotFound
}

// memPresenterStore is the part of openid4vppresenter's transaction store the
// QERDS receive and approval path uses.
type memPresenterStore struct {
	mu   sync.Mutex
	rows map[uuid.UUID]openid4vppresenter.Transaction
}

func newMemPresenterStore() *memPresenterStore {
	return &memPresenterStore{rows: map[uuid.UUID]openid4vppresenter.Transaction{}}
}

func (s *memPresenterStore) Create(context.Context, openid4vppresenter.NewTransaction) (string, error) {
	panic("browser flow not exercised")
}

func (s *memPresenterStore) Get(context.Context, string) (openid4vppresenter.Transaction, error) {
	return openid4vppresenter.Transaction{}, openid4vppresenter.ErrNotFound
}

func (s *memPresenterStore) BindUser(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (s *memPresenterStore) SelectOrganization(context.Context, uuid.UUID, uuid.UUID, string) (openid4vppresenter.Transaction, error) {
	panic("browser flow not exercised")
}

func (s *memPresenterStore) CreateForOrganization(_ context.Context, orgID, sourceMessageID uuid.UUID, in openid4vppresenter.NewTransaction) (openid4vppresenter.Transaction, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.rows {
		if t.SourceMessageID != nil && *t.SourceMessageID == sourceMessageID {
			return t, false, nil
		}
	}
	t := openid4vppresenter.Transaction{
		ID: uuid.New(), ClientID: in.ClientID, RequestURI: in.RequestURI, VerifierIdentity: in.Request.VerifierIdentity,
		DCQLQuery: in.Request.DCQLQuery, Nonce: in.Request.Nonce, State: in.Request.State, ResponseURI: in.Request.ResponseURI,
		ResponseMode: in.Request.ResponseMode, RequestObject: in.Request.Raw, Status: openid4vppresenter.StatusOrgSelected,
		OrganizationID: &orgID, SourceMessageID: &sourceMessageID, ExpiresAt: time.Now().Add(e2eTTL),
	}
	s.rows[t.ID] = t
	return t, true, nil
}

func (s *memPresenterStore) ListPendingForOrg(_ context.Context, orgID uuid.UUID) ([]openid4vppresenter.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []openid4vppresenter.Transaction
	for _, t := range s.rows {
		if t.Status == openid4vppresenter.StatusOrgSelected && *t.OrganizationID == orgID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *memPresenterStore) ClaimPendingForOrg(_ context.Context, orgID, id uuid.UUID) (openid4vppresenter.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.rows[id]
	if !ok || t.Status != openid4vppresenter.StatusOrgSelected || *t.OrganizationID != orgID {
		return openid4vppresenter.Transaction{}, openid4vppresenter.ErrNotPending
	}
	t.Status = "claimed"
	s.rows[id] = t
	return t, nil
}

func (s *memPresenterStore) settle(id uuid.UUID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.rows[id]
	if !ok {
		return openid4vppresenter.ErrNotPending
	}
	t.Status = status
	s.rows[id] = t
	return nil
}

func (s *memPresenterStore) Complete(_ context.Context, id uuid.UUID) error {
	return s.settle(id, openid4vppresenter.StatusCompleted)
}

func (s *memPresenterStore) Deny(_ context.Context, id uuid.UUID, _ string) error {
	return s.settle(id, openid4vppresenter.StatusDenied)
}

func (s *memPresenterStore) DenyPendingForOrg(_ context.Context, _, id uuid.UUID, _ string) error {
	return s.settle(id, openid4vppresenter.StatusDenied)
}
