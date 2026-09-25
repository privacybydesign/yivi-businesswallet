package openid4vppresenter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type fakeStore struct {
	created []NewTransaction
	// orgTransactions holds every row CreateForOrganization queued, keyed by id
	// — enough for a QERDS test to inspect what landed without a Service method
	// to read it back through (that read belongs to whatever lists an org's
	// org_selected queue; #113's governance layer, not this seam).
	orgTransactions map[uuid.UUID]Transaction
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
func (*fakeStore) Complete(context.Context, uuid.UUID) error     { return nil }
func (*fakeStore) Deny(context.Context, uuid.UUID, string) error { return nil }

func (f *fakeStore) CreateForOrganization(_ context.Context, orgID, sourceMessageID uuid.UUID, in NewTransaction) (Transaction, bool, error) {
	for _, existing := range f.orgTransactions {
		if existing.OrganizationID != nil && *existing.OrganizationID == orgID &&
			existing.SourceMessageID != nil && *existing.SourceMessageID == sourceMessageID {
			return existing, false, nil
		}
	}
	f.created = append(f.created, in)
	id := uuid.New()
	t := Transaction{
		ID: id, ClientID: in.ClientID, RequestURI: in.RequestURI, RequestURIMethod: in.RequestURIMethod,
		VerifierIdentity: in.Request.VerifierIdentity, DCQLQuery: in.Request.DCQLQuery, Nonce: in.Request.Nonce,
		ResponseURI: in.Request.ResponseURI, ResponseMode: in.Request.ResponseMode, RequestObject: in.Request.Raw,
		Status: StatusOrgSelected, OrganizationID: &orgID, SourceMessageID: &sourceMessageID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if f.orgTransactions == nil {
		f.orgTransactions = map[uuid.UUID]Transaction{}
	}
	f.orgTransactions[id] = t
	return t, true, nil
}

// forOrg returns the rows CreateForOrganization queued for orgID, for tests to
// assert on directly.
func (f *fakeStore) forOrg(orgID uuid.UUID) []Transaction {
	var out []Transaction
	for _, t := range f.orgTransactions {
		if t.OrganizationID != nil && *t.OrganizationID == orgID {
			out = append(out, t)
		}
	}
	return out
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

// ReceiveFromQERDS shares Start's invocation validation, so a QERDS-originated
// request is rejected the same way — before it is queued for anyone to decide.
func TestReceiveFromQERDSRejectsAmbiguousForms(t *testing.T) {
	svc, store, fetcher := newTestService()
	_, _, err := svc.ReceiveFromQERDS(context.Background(), uuid.New(), uuid.New(), StartRequest{ClientID: testClientID})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidRequest)
	}
	if fetcher.called || len(store.created) != 0 {
		t.Fatal("rejected request reached the fetcher or the store")
	}
}

// A valid invocation lands org-bound at org_selected, skipping the browser's
// pending_auth/org-picker steps entirely, and is idempotent on the source
// message.
func TestReceiveFromQERDSQueuesOrgBound(t *testing.T) {
	svc, _, _ := newTestService()
	orgID, msgID := uuid.New(), uuid.New()
	req := StartRequest{ClientID: testClientID, RequestURI: "https://v/req"}

	t1, recorded, err := svc.ReceiveFromQERDS(context.Background(), orgID, msgID, req)
	if err != nil {
		t.Fatalf("ReceiveFromQERDS: %v", err)
	}
	if !recorded {
		t.Fatal("first delivery must be recorded")
	}
	if t1.Status != StatusOrgSelected || t1.OrganizationID == nil || *t1.OrganizationID != orgID {
		t.Fatalf("queued transaction = %+v, want org-bound org_selected", t1)
	}

	if _, recorded, err := svc.ReceiveFromQERDS(context.Background(), orgID, msgID, req); err != nil || recorded {
		t.Fatalf("re-delivery: recorded=%v err=%v, want recorded=false err=nil", recorded, err)
	}
}
