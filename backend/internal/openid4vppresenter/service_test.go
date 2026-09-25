package openid4vppresenter

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type fakeStore struct {
	created []NewTransaction
	// pending backs GetPendingForOrg/ListPendingForOrg; Complete and Deny remove
	// an entry the same way the real store's status guard does, so a test can
	// tell a decided transaction from one still awaiting approval.
	pending   map[uuid.UUID]Transaction
	completed []uuid.UUID
	denied    []uuid.UUID
}

func (f *fakeStore) Create(_ context.Context, in NewTransaction) (string, error) {
	f.created = append(f.created, in)
	return "opaque", nil
}

func (*fakeStore) Get(context.Context, string) (Transaction, error) {
	return Transaction{}, ErrNotFound
}
func (*fakeStore) BindUser(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (*fakeStore) SelectOrganization(context.Context, uuid.UUID, uuid.UUID, string) (Transaction, error) {
	return Transaction{}, nil
}

func (f *fakeStore) Complete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.pending[id]; !ok {
		return ErrNotPending
	}
	delete(f.pending, id)
	f.completed = append(f.completed, id)
	return nil
}

func (f *fakeStore) Deny(_ context.Context, id uuid.UUID, _ string) error {
	if _, ok := f.pending[id]; !ok {
		return ErrNotPending
	}
	delete(f.pending, id)
	f.denied = append(f.denied, id)
	return nil
}

func (f *fakeStore) GetPendingForOrg(_ context.Context, orgID, id uuid.UUID) (Transaction, error) {
	t, ok := f.pending[id]
	if !ok || t.OrganizationID == nil || *t.OrganizationID != orgID {
		return Transaction{}, ErrNotPending
	}
	return t, nil
}

func (f *fakeStore) ListPendingForOrg(_ context.Context, orgID uuid.UUID) ([]Transaction, error) {
	out := []Transaction{}
	for _, t := range f.pending {
		if t.OrganizationID != nil && *t.OrganizationID == orgID {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeOrgs struct{}

func (fakeOrgs) ListForUser(context.Context, uuid.UUID) ([]organization.Organization, error) {
	return nil, nil
}

type fakeFetcher struct {
	called bool
}

func (f *fakeFetcher) Fetch(context.Context, string) ([]byte, error) {
	f.called = true
	return []byte("h.p.s"), nil
}

type fakeValidator struct{}

func (fakeValidator) Validate(_ context.Context, clientID string, _ []byte) (RequestObject, error) {
	return RequestObject{ClientID: clientID, VerifierIdentity: "v", Nonce: "n", ResponseURI: "https://v/r", ResponseMode: "direct_post", DCQLQuery: []byte(`{}`)}, nil
}
func (fakeValidator) ClientIDPrefixes() []string { return nil }

type fakeResponder struct{}

func (fakeResponder) Send(context.Context, Transaction, openid4vp.VpToken) (string, error) {
	return "", nil
}

func newTestService() (*Service, *fakeStore, *fakeFetcher) {
	store, fetcher := &fakeStore{}, &fakeFetcher{}
	svc := NewService(store, fakeOrgs{}, eudiholder.NewStubHolder(), fetcher, fakeValidator{}, fakeResponder{}, false)
	return svc, store, fetcher
}

// The ambiguous / unsupported invocation forms are rejected before any network
// I/O and before a row is written — the design's "never a silent guess".
func TestStartRejectsAmbiguousForms(t *testing.T) {
	cases := []struct {
		name    string
		req     StartRequest
		wantErr error
	}{
		{"missing client_id", StartRequest{RequestURI: "https://v/req"}, ErrInvalidRequest},
		{"neither", StartRequest{ClientID: testClientID}, ErrInvalidRequest},
		{"both", StartRequest{ClientID: testClientID, RequestURI: "https://v/req", Request: "h.p.s"}, ErrInvalidRequest},
		{"by value", StartRequest{ClientID: testClientID, Request: "h.p.s"}, ErrInvalidRequest},
		{"bad method", StartRequest{ClientID: testClientID, RequestURI: "https://v/req", RequestURIMethod: "put"}, ErrInvalidRequestURIMethod},
		{"case-sensitive method", StartRequest{ClientID: testClientID, RequestURI: "https://v/req", RequestURIMethod: "GET"}, ErrInvalidRequestURIMethod},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, fetcher := newTestService()
			_, err := svc.Start(context.Background(), tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if fetcher.called || len(store.created) != 0 {
				t.Fatal("rejected request reached the fetcher or the store")
			}
		})
	}
}

func TestStartAcceptsGetAndPostMethods(t *testing.T) {
	for _, method := range []string{"", "get", "post"} {
		svc, store, _ := newTestService()
		id, err := svc.Start(context.Background(), StartRequest{ClientID: testClientID, RequestURI: "https://v/req", RequestURIMethod: method})
		if err != nil {
			t.Fatalf("method %q: %v", method, err)
		}
		if id != "opaque" || len(store.created) != 1 {
			t.Fatalf("method %q: transaction not created", method)
		}
		want := method
		if want == "" {
			want = "get"
		}
		if store.created[0].RequestURIMethod != want {
			t.Errorf("method %q persisted as %q", method, store.created[0].RequestURIMethod)
		}
	}
}

// The governance layer (#113): a pending transaction only moves once an admin
// of the organization it was selected for decides.

func TestApproveCompletesAPendingTransaction(t *testing.T) {
	svc, store, _ := newTestService()
	orgID := uuid.New()
	id := uuid.New()
	store.pending = map[uuid.UUID]Transaction{
		id: {ID: id, OrganizationID: &orgID, ClientID: testClientID, DCQLQuery: []byte(`{}`)},
	}

	if _, err := svc.Approve(context.Background(), orgID, id); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if len(store.completed) != 1 || store.completed[0] != id {
		t.Fatalf("completed = %v, want [%s]", store.completed, id)
	}

	// Settled: a second approve finds nothing pending, not a second completion.
	if _, err := svc.Approve(context.Background(), orgID, id); !errors.Is(err, ErrNotPending) {
		t.Fatalf("re-approving a decided transaction = %v, want ErrNotPending", err)
	}
}

func TestApproveIsScopedToTheOwningOrganization(t *testing.T) {
	svc, store, _ := newTestService()
	orgID, otherOrg := uuid.New(), uuid.New()
	id := uuid.New()
	store.pending = map[uuid.UUID]Transaction{
		id: {ID: id, OrganizationID: &orgID, ClientID: testClientID, DCQLQuery: []byte(`{}`)},
	}

	if _, err := svc.Approve(context.Background(), otherOrg, id); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approving another organization's transaction = %v, want ErrNotPending", err)
	}
	if len(store.completed) != 0 {
		t.Fatal("a cross-organization approve must not complete anything")
	}
}

func TestDenyRefusesAPendingTransaction(t *testing.T) {
	svc, store, _ := newTestService()
	orgID := uuid.New()
	id := uuid.New()
	store.pending = map[uuid.UUID]Transaction{id: {ID: id, OrganizationID: &orgID}}

	if err := svc.Deny(context.Background(), orgID, id); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if len(store.denied) != 1 || store.denied[0] != id {
		t.Fatalf("denied = %v, want [%s]", store.denied, id)
	}
	if len(store.pending) != 0 {
		t.Fatal("a denied transaction must leave the pending queue")
	}
}

func TestPendingApprovalsListsOnlyTheCallingOrganization(t *testing.T) {
	svc, store, _ := newTestService()
	orgID, otherOrg := uuid.New(), uuid.New()
	mine, theirs := uuid.New(), uuid.New()
	store.pending = map[uuid.UUID]Transaction{
		mine:   {ID: mine, OrganizationID: &orgID},
		theirs: {ID: theirs, OrganizationID: &otherOrg},
	}

	got, err := svc.PendingApprovals(context.Background(), orgID)
	if err != nil {
		t.Fatalf("PendingApprovals: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine {
		t.Fatalf("PendingApprovals(orgID) = %+v, want just %s", got, mine)
	}
}
