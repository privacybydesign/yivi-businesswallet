package openid4vppresenter

import (
	"context"
	"errors"
	"sync"
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
	// mu guards pending/claimed/completed/denied: TestApproveDoesNotDeliverTwice
	// drives a concurrent Approve call at this store, mirroring the real one's
	// atomic UPDATE.
	mu sync.Mutex
	// pending backs DenyPendingForOrg/ListPendingForOrg; claimed holds what
	// ClaimPendingForOrg moved out of pending, the fake's stand-in for the real
	// store's statusApproving row. Complete and Deny remove an entry from
	// whichever of the two holds it, the same way the real store's status guard
	// accepts either, so a test can tell a decided transaction from one still
	// awaiting approval.
	pending   map[uuid.UUID]Transaction
	claimed   map[uuid.UUID]Transaction
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.consume(id) {
		return ErrNotPending
	}
	f.completed = append(f.completed, id)
	return nil
}

func (f *fakeStore) Deny(_ context.Context, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.consume(id) {
		return ErrNotPending
	}
	f.denied = append(f.denied, id)
	return nil
}

// consume removes id from whichever of pending/claimed holds it, mirroring the
// real store's consume() accepting either pre-terminal status. Caller holds mu.
func (f *fakeStore) consume(id uuid.UUID) bool {
	if _, ok := f.pending[id]; ok {
		delete(f.pending, id)
		return true
	}
	if _, ok := f.claimed[id]; ok {
		delete(f.claimed, id)
		return true
	}
	return false
}

// DenyPendingForOrg is the fake's atomic stand-in for the real store's
// UPDATE ... WHERE status = org_selected: like ClaimPendingForOrg, it only
// matches a row still in pending, never one ClaimPendingForOrg already moved
// to claimed.
func (f *fakeStore) DenyPendingForOrg(_ context.Context, orgID, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.pending[id]
	if !ok || t.OrganizationID == nil || *t.OrganizationID != orgID {
		return ErrNotPending
	}
	delete(f.pending, id)
	f.denied = append(f.denied, id)
	return nil
}

// ClaimPendingForOrg is the fake's atomic stand-in for the real store's
// UPDATE ... WHERE status = org_selected: mu makes the check-and-move a single
// step, so two goroutines racing on the same id cannot both see it pending.
func (f *fakeStore) ClaimPendingForOrg(_ context.Context, orgID, id uuid.UUID) (Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.pending[id]
	if !ok || t.OrganizationID == nil || *t.OrganizationID != orgID {
		return Transaction{}, ErrNotPending
	}
	delete(f.pending, id)
	if f.claimed == nil {
		f.claimed = map[uuid.UUID]Transaction{}
	}
	f.claimed[id] = t
	return t, nil
}

func (f *fakeStore) ListPendingForOrg(_ context.Context, orgID uuid.UUID) ([]Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Transaction{}
	for _, t := range f.pending {
		if t.OrganizationID != nil && *t.OrganizationID == orgID {
			out = append(out, t)
		}
	}
	return out, nil
}

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

// blockingResponder lets TestApproveDoesNotDeliverTwiceToASlowerConcurrentCall
// pause a first Approve call at the exact instant it starts delivering to the
// verifier, then drive a second Approve call on the same transaction into that
// window deterministically — a stand-in for a truly concurrent second caller,
// without depending on goroutine scheduling to land in the same window.
type blockingResponder struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (r *blockingResponder) Send(context.Context, Transaction, openid4vp.VpToken) (string, error) {
	r.mu.Lock()
	r.calls++
	first := r.calls == 1
	r.mu.Unlock()
	if first {
		close(r.entered)
		<-r.release
	}
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

// A plain read-then-act GetPendingForOrg let a second, concurrent Approve call
// on the same transaction pass the check and reach the verifier before the
// first call's delivery had committed anything back to the store. This pauses
// a first Approve call at the instant it starts delivering — deterministically,
// rather than racing goroutine scheduling to land two calls in that window —
// and drives a second Approve call on the identical transaction into it: with
// the fix (ClaimPendingForOrg) that second call finds nothing pending and never
// reaches the verifier; before it, both did.
func TestApproveDoesNotDeliverTwiceToASlowerConcurrentCall(t *testing.T) {
	store := &fakeStore{}
	responder := &blockingResponder{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(store, fakeOrgs{}, eudiholder.NewStubHolder(), &fakeFetcher{}, fakeValidator{}, responder, false)
	orgID, id := uuid.New(), uuid.New()
	store.pending = map[uuid.UUID]Transaction{
		id: {ID: id, OrganizationID: &orgID, ClientID: testClientID, DCQLQuery: []byte(`{}`)},
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.Approve(context.Background(), orgID, id)
		firstDone <- err
	}()
	<-responder.entered // the first call has read/claimed and is mid-delivery

	_, secondErr := svc.Approve(context.Background(), orgID, id)
	close(responder.release)
	firstErr := <-firstDone

	if responder.calls != 1 {
		t.Fatalf("verifier deliveries = %d, want exactly 1 (no double delivery)", responder.calls)
	}
	if !errors.Is(secondErr, ErrNotPending) {
		t.Fatalf("second, concurrent Approve = %v, want ErrNotPending", secondErr)
	}
	if firstErr != nil {
		t.Fatalf("first Approve: %v, want nil", firstErr)
	}
}

// racyDenyStore delays Deny's atomic write until the test releases it, so a
// concurrent Approve's claim can be driven into that window regardless of when
// Deny's call was made — proving the write itself, not just a timing accident,
// rejects a row Approve has already claimed.
type racyDenyStore struct {
	*fakeStore
	writing chan struct{}
	resume  chan struct{}
}

func (s *racyDenyStore) DenyPendingForOrg(ctx context.Context, orgID, id uuid.UUID, reason string) error {
	close(s.writing)
	<-s.resume
	return s.fakeStore.DenyPendingForOrg(ctx, orgID, id, reason)
}

// Before the fix, Deny's precondition check (GetPendingForOrg) only matched
// org_selected, but its write (Store.Deny) also matched approving. A
// concurrent Approve claiming the row between Deny's check and its write let
// Deny mark the transaction denied+consumed after Approve had already started
// delivering it to the verifier — so Approve then found its own row gone and
// reported an error despite the verifier having received a valid response.
// DenyPendingForOrg's single atomic write, scoped to org_selected only, closes
// that window: it is delayed here past Approve's claim and still refuses.
func TestDenyDoesNotRaceAConcurrentApprove(t *testing.T) {
	store := &fakeStore{}
	racy := &racyDenyStore{fakeStore: store, writing: make(chan struct{}), resume: make(chan struct{})}
	responder := &blockingResponder{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(racy, fakeOrgs{}, eudiholder.NewStubHolder(), &fakeFetcher{}, fakeValidator{}, responder, false)
	orgID, id := uuid.New(), uuid.New()
	store.pending = map[uuid.UUID]Transaction{
		id: {ID: id, OrganizationID: &orgID, ClientID: testClientID, DCQLQuery: []byte(`{}`)},
	}

	denyErr := make(chan error, 1)
	go func() {
		denyErr <- svc.Deny(context.Background(), orgID, id)
	}()
	<-racy.writing // Deny has been called and is about to run its write

	approveErr := make(chan error, 1)
	go func() {
		_, err := svc.Approve(context.Background(), orgID, id)
		approveErr <- err
	}()
	<-responder.entered // Approve has claimed the row and is mid-delivery to the verifier

	close(racy.resume) // let Deny's write run now, against the claimed, in-flight row
	if err := <-denyErr; !errors.Is(err, ErrNotPending) {
		t.Fatalf("Deny racing a concurrent Approve = %v, want ErrNotPending", err)
	}

	close(responder.release)
	if err := <-approveErr; err != nil {
		t.Fatalf("Approve: %v, want nil — the verifier already received its response", err)
	}
	if len(store.completed) != 1 || store.completed[0] != id {
		t.Fatalf("completed = %v, want [%s]", store.completed, id)
	}
	if len(store.denied) != 0 {
		t.Fatal("Deny must not consume a transaction a concurrent Approve already claimed")
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
