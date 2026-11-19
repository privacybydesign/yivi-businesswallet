package openid4vprequester_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vppresenter"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/relyingparty"
)

// memStore mirrors the real store's guards: the Request Object is handed out
// once, and only an open sent request can be settled.
type memStore struct {
	mu   sync.Mutex
	rows map[uuid.UUID]openid4vprequester.Request
}

func newMemStore() *memStore {
	return &memStore{rows: map[uuid.UUID]openid4vprequester.Request{}}
}

func (s *memStore) get(id uuid.UUID) openid4vprequester.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[id]
}

func (s *memStore) Create(_ context.Context, in openid4vprequester.NewRequest) (openid4vprequester.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := openid4vprequester.Request{
		ID: in.ID, OrganizationID: in.OrganizationID, CreatedBy: in.CreatedBy, SenderAddress: in.SenderAddress,
		RecipientAddress: in.RecipientAddress, ClientID: in.ClientID, Nonce: in.Nonce, State: in.State,
		Credentials: in.Credentials, RequestObject: in.RequestObject, Status: openid4vprequester.StatusSent,
		CreatedAt: time.Now(), ExpiresAt: in.ExpiresAt,
	}
	s.rows[r.ID] = r
	return r, nil
}

func (s *memStore) SetMessage(_ context.Context, id, messageID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	r.QerdsMessageID = &messageID
	s.rows[id] = r
	return nil
}

func (s *memStore) Get(_ context.Context, id uuid.UUID) (openid4vprequester.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return openid4vprequester.Request{}, openid4vprequester.ErrNotFound
	}
	return r, nil
}

func (s *memStore) GetForOrg(ctx context.Context, orgID, id uuid.UUID) (openid4vprequester.Request, error) {
	r, err := s.Get(ctx, id)
	if err != nil || r.OrganizationID != orgID {
		return openid4vprequester.Request{}, openid4vprequester.ErrNotFound
	}
	return r, nil
}

func (s *memStore) ListForOrg(_ context.Context, orgID uuid.UUID) ([]openid4vprequester.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []openid4vprequester.Request
	for _, r := range s.rows {
		if r.OrganizationID == orgID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *memStore) FetchRequestObject(_ context.Context, id uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok || r.RequestFetchedAt != nil || r.Status != openid4vprequester.StatusSent || time.Now().After(r.ExpiresAt) {
		return "", openid4vprequester.ErrNotFound
	}
	now := time.Now()
	r.RequestFetchedAt = &now
	s.rows[id] = r
	return r.RequestObject, nil
}

func (s *memStore) settle(id uuid.UUID, status string, reason *string, disclosed []openid4vprequester.DisclosedCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok || r.Status != openid4vprequester.StatusSent || time.Now().After(r.ExpiresAt) {
		return openid4vprequester.ErrNotPending
	}
	r.Status, r.FailureReason, r.Disclosed = status, reason, disclosed
	s.rows[id] = r
	return nil
}

func (s *memStore) Complete(_ context.Context, id uuid.UUID, disclosed []openid4vprequester.DisclosedCredential) error {
	return s.settle(id, openid4vprequester.StatusCompleted, nil, disclosed)
}

func (s *memStore) Fail(_ context.Context, id uuid.UUID, reason string) error {
	return s.settle(id, openid4vprequester.StatusFailed, &reason, nil)
}

// fakeQerds records what was sent; failSend makes the provider leg fail.
type fakeQerds struct {
	failSend bool
	sent     []string
}

func (f *fakeQerds) ResolveSender(_ context.Context, orgID uuid.UUID, from string) (qerds.Address, error) {
	if from != "" && from != requesterAddress {
		return qerds.Address{}, qerds.ErrSenderNotOwned
	}
	return qerds.Address{OrganizationID: orgID, Address: requesterAddress, IsDefault: true}, nil
}

func (f *fakeQerds) Send(_ context.Context, _ uuid.UUID, _, recipient, _, body string, _ []qerdsprovider.Attachment) (qerds.Message, error) {
	if f.failSend {
		return qerds.Message{}, errors.New("qerds: resolve recipient: unknown")
	}
	f.sent = append(f.sent, body)
	return qerds.Message{ID: uuid.New(), RecipientAddress: recipient}, nil
}

// scriptedVerifier returns fixed presentations whatever the token.
type scriptedVerifier []relyingparty.Presented

func (v scriptedVerifier) Verify(map[string][]string, string, string) []relyingparty.Presented {
	return v
}

func newTestService(t *testing.T, q *fakeQerds, v scriptedVerifier) (*openid4vprequester.Service, *memStore) {
	t.Helper()
	ca, err := relyingparty.NewCA("Test CA")
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	return openid4vprequester.NewService(store, q, ca, v, "https://wallet.test/", e2eTTL), store
}

var testOrg = organization.Organization{ID: uuid.New(), Name: "Acme B.V."}

func send(t *testing.T, svc *openid4vprequester.Service, creds ...openid4vprequester.CredentialRequest) openid4vprequester.Request {
	t.Helper()
	if len(creds) == 0 {
		creds = []openid4vprequester.CredentialRequest{{VCT: requestedVCT, Claims: []string{"legalName"}}}
	}
	r, err := svc.Send(context.Background(), testOrg, uuid.New(), openid4vprequester.SendInput{Recipient: holderAddress, Credentials: creds})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return r
}

// The envelope carries the org's certified client_id and this deployment's
// request_uri, and the signed request certifies the sending address.
func TestSendCertifiesTheSendingAddress(t *testing.T) {
	q := &fakeQerds{}
	svc, _ := newTestService(t, q, nil)
	r := send(t, svc)

	if len(q.sent) != 1 {
		t.Fatalf("sent %d QERDS messages, want 1", len(q.sent))
	}
	env, ok := openid4vppresenter.ParsePresentationRequestEnvelope(q.sent[0])
	if !ok {
		t.Fatalf("body is not a presentation-request envelope: %s", q.sent[0])
	}
	if env.ClientID != r.ClientID || !strings.HasPrefix(env.ClientID, "x509_hash:") {
		t.Errorf("envelope client_id = %q, want the request's x509_hash %q", env.ClientID, r.ClientID)
	}
	if want := "https://wallet.test/api/v1/openid4vp/outbound/" + r.ID.String() + "/request-object"; env.RequestURI != want {
		t.Errorf("request_uri = %q, want %q", env.RequestURI, want)
	}
	if env.SenderOrgName != testOrg.Name || r.SenderAddress != requesterAddress {
		t.Errorf("sender = %q/%q", env.SenderOrgName, r.SenderAddress)
	}
	if len(r.Credentials) != 1 || r.Credentials[0].ID != "credential_1" {
		t.Errorf("credentials = %+v, want server-assigned query ids", r.Credentials)
	}
}

func TestSendRejectsInvalidInput(t *testing.T) {
	cases := map[string]openid4vprequester.SendInput{
		"no recipient":      {Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}}},
		"no credentials":    {Recipient: holderAddress},
		"blank vct":         {Recipient: holderAddress, Credentials: []openid4vprequester.CredentialRequest{{VCT: "  "}}},
		"vct with a space":  {Recipient: holderAddress, Credentials: []openid4vprequester.CredentialRequest{{VCT: "nl kvk"}}},
		"blank claim name":  {Recipient: holderAddress, Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT, Claims: []string{""}}}},
		"too many requests": {Recipient: holderAddress, Credentials: make([]openid4vprequester.CredentialRequest, 11)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			q := &fakeQerds{}
			svc, store := newTestService(t, q, nil)
			_, err := svc.Send(context.Background(), testOrg, uuid.New(), in)
			if !errors.Is(err, openid4vprequester.ErrInvalidInput) {
				t.Errorf("err = %v, want %v", err, openid4vprequester.ErrInvalidInput)
			}
			if len(q.sent) != 0 || len(store.rows) != 0 {
				t.Error("an invalid request was stored or sent")
			}
		})
	}
}

func TestSendRejectsASenderTheOrgDoesNotOwn(t *testing.T) {
	svc, _ := newTestService(t, &fakeQerds{}, nil)
	_, err := svc.Send(context.Background(), testOrg, uuid.New(), openid4vprequester.SendInput{
		From: "someone@else.test", Recipient: holderAddress,
		Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}},
	})
	if !errors.Is(err, qerds.ErrSenderNotOwned) {
		t.Errorf("err = %v, want %v", err, qerds.ErrSenderNotOwned)
	}
}

// A request whose invocation never left is marked failed, not left as sent.
func TestSendMarksAnUndeliveredRequestFailed(t *testing.T) {
	svc, store := newTestService(t, &fakeQerds{failSend: true}, nil)
	_, err := svc.Send(context.Background(), testOrg, uuid.New(), openid4vprequester.SendInput{
		Recipient: holderAddress, Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}},
	})
	if !errors.Is(err, openid4vprequester.ErrDeliveryFailed) {
		t.Fatalf("err = %v, want %v", err, openid4vprequester.ErrDeliveryFailed)
	}
	for _, r := range store.rows {
		if r.Status != openid4vprequester.StatusFailed || r.FailureReason == nil || *r.FailureReason != openid4vprequester.ReasonDeliveryFailed {
			t.Errorf("request = %s/%v, want failed with %s", r.Status, r.FailureReason, openid4vprequester.ReasonDeliveryFailed)
		}
	}
}

func TestRequestObjectIsServedOnce(t *testing.T) {
	svc, _ := newTestService(t, &fakeQerds{}, nil)
	r := send(t, svc)
	jar, err := svc.RequestObject(context.Background(), r.ID)
	if err != nil || jar != r.RequestObject {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := svc.RequestObject(context.Background(), r.ID); !errors.Is(err, openid4vprequester.ErrNotFound) {
		t.Errorf("second fetch: err = %v, want %v", err, openid4vprequester.ErrNotFound)
	}
}

func answer(state string) url.Values {
	return url.Values{"state": {state}, "vp_token": {`{"credential_1":["sd-jwt~"]}`}}
}

func verified(queryID, vct string) relyingparty.Presented {
	return relyingparty.Presented{QueryID: queryID, VCT: vct, Verified: true, Claims: map[string]any{"legalName": "Globex"}}
}

// Without the request's state a post is refused and changes nothing: the id is
// not a secret, so posting to it must not burn the request.
func TestRespondWithoutStateLeavesTheRequestOpen(t *testing.T) {
	svc, store := newTestService(t, &fakeQerds{}, scriptedVerifier{verified("credential_1", requestedVCT)})
	r := send(t, svc)
	if err := svc.Respond(context.Background(), r.ID, answer("guess")); !errors.Is(err, openid4vprequester.ErrInvalidResponse) {
		t.Fatalf("err = %v, want %v", err, openid4vprequester.ErrInvalidResponse)
	}
	if got := store.get(r.ID); got.Status != openid4vprequester.StatusSent {
		t.Fatalf("status = %q, want still sent", got.Status)
	}
	if err := svc.Respond(context.Background(), r.ID, answer(r.State)); err != nil {
		t.Fatalf("the real answer after a bad post: %v", err)
	}
}

func TestRespondCompletesOnceWithTheVerifiedDisclosure(t *testing.T) {
	svc, store := newTestService(t, &fakeQerds{}, scriptedVerifier{verified("credential_1", requestedVCT)})
	r := send(t, svc)
	if err := svc.Respond(context.Background(), r.ID, answer(r.State)); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	got := store.get(r.ID)
	if got.Status != openid4vprequester.StatusCompleted || len(got.Disclosed) != 1 || got.Disclosed[0].Claims["legalName"] != "Globex" {
		t.Fatalf("request = %s %+v", got.Status, got.Disclosed)
	}
	if err := svc.Respond(context.Background(), r.ID, answer(r.State)); !errors.Is(err, openid4vprequester.ErrNotPending) {
		t.Errorf("second answer: err = %v, want %v", err, openid4vprequester.ErrNotPending)
	}
}

// An answer with the right state that cannot be accepted consumes the request
// as failed, and keeps nothing it carried.
func TestRespondRefusesAnUnacceptableAnswer(t *testing.T) {
	cases := []struct {
		name   string
		v      scriptedVerifier
		creds  []openid4vprequester.CredentialRequest
		reason string
	}{
		{"signature or key binding fails", scriptedVerifier{{QueryID: "credential_1", VCT: requestedVCT, Error: "bad kb-jwt"}}, nil, openid4vprequester.ReasonVerificationFailed},
		{"another credential type", scriptedVerifier{verified("credential_1", "nl.other.type")}, nil, openid4vprequester.ReasonVerificationFailed},
		{
			"a requested credential is missing",
			scriptedVerifier{verified("credential_1", requestedVCT)},
			[]openid4vprequester.CredentialRequest{{VCT: requestedVCT}, {VCT: "nl.second.type"}},
			openid4vprequester.ReasonIncompleteResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newTestService(t, &fakeQerds{}, tc.v)
			r := send(t, svc, tc.creds...)
			if err := svc.Respond(context.Background(), r.ID, answer(r.State)); !errors.Is(err, openid4vprequester.ErrInvalidResponse) {
				t.Fatalf("err = %v, want %v", err, openid4vprequester.ErrInvalidResponse)
			}
			got := store.get(r.ID)
			if got.Status != openid4vprequester.StatusFailed || got.FailureReason == nil || *got.FailureReason != tc.reason {
				t.Errorf("request = %s/%v, want failed with %s", got.Status, got.FailureReason, tc.reason)
			}
			if len(got.Disclosed) != 0 {
				t.Errorf("a refused answer left a disclosure: %+v", got.Disclosed)
			}
		})
	}
}

func TestRespondToAnExpiredRequestIsRefused(t *testing.T) {
	svc, store := newTestService(t, &fakeQerds{}, scriptedVerifier{verified("credential_1", requestedVCT)})
	r := send(t, svc)
	expired := store.get(r.ID)
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	store.rows[r.ID] = expired
	if err := svc.Respond(context.Background(), r.ID, answer(r.State)); !errors.Is(err, openid4vprequester.ErrNotPending) {
		t.Errorf("err = %v, want %v", err, openid4vprequester.ErrNotPending)
	}
	if got := expired.EffectiveStatus(time.Now()); got != openid4vprequester.StatusExpired {
		t.Errorf("effective status = %q, want expired", got)
	}
}
