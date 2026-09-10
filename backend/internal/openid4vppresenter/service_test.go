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
