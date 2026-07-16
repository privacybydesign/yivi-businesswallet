package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	presentationstore "github.com/privacybydesign/yivi-businesswallet/backend/internal/presentation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakeVerifier is the test double for the verifier seam, exercising the handler
// without a live EUDI verifier.
type fakeVerifier struct {
	startSession openid4vpverifier.Session
	startErr     error
	statusVal    string
	statusErr    error
	resultVal    openid4vpverifier.Presentation
	resultErr    error
	started      bool
}

func (f *fakeVerifier) StartPresentation(_ context.Context, _ openid4vpverifier.Scope) (openid4vpverifier.Session, error) {
	f.started = true
	return f.startSession, f.startErr
}

func (f *fakeVerifier) Result(_ context.Context, _ string) (openid4vpverifier.Presentation, error) {
	return f.resultVal, f.resultErr
}

func (f *fakeVerifier) Status(_ context.Context, _ string) (string, error) {
	return f.statusVal, f.statusErr
}

// fakePresentations stands in for the presentation store: Create hands back the
// client-facing id, and TransactionID resolves it (or reports ErrNotFound).
type fakePresentations struct {
	createID  string
	createErr error
	txID      string
	lookupErr error
}

func (f *fakePresentations) Create(_ context.Context, _ string) (string, error) {
	return f.createID, f.createErr
}

func (f *fakePresentations) TransactionID(_ context.Context, _ string) (string, error) {
	return f.txID, f.lookupErr
}

func newTestHandler(v verifier, p presentations) *Handler {
	return &Handler{
		svc:    NewService(v, p, nil, nil, nil),
		cookie: CookieConfig{},
	}
}

func TestMeReportsPlatformAdminStatus(t *testing.T) {
	admins := NewPlatformAdmins([]string{"admin@yivi.app"})
	tests := []struct {
		name  string
		email user.Email
		want  bool
	}{
		{name: "platform admin", email: "admin@yivi.app", want: true},
		{name: "regular member", email: "user@yivi.app", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{admins: admins}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/me", nil)
			req = req.WithContext(ContextWithUser(req.Context(), user.User{Email: tc.email}))

			if err := h.me(rec, req); err != nil {
				t.Fatalf("me: %v", err)
			}

			var body meResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.IsPlatformAdmin != tc.want {
				t.Errorf("isPlatformAdmin = %v, want %v", body.IsPlatformAdmin, tc.want)
			}
			if body.Email != string(tc.email) {
				t.Errorf("email = %q, want %q", body.Email, tc.email)
			}
		})
	}
}

func TestStartSessionReturnsWalletLink(t *testing.T) {
	fake := &fakeVerifier{
		startSession: openid4vpverifier.Session{TransactionID: "tx-abc", WalletLink: "openid4vp://?request_uri=https%3A%2F%2Fv.example"},
	}
	// The client receives the store's opaque id, never the verifier transaction_id.
	h := newTestHandler(fake, &fakePresentations{createID: "opaque-id"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/session", nil)
	if err := h.startSession(rec, req); err != nil {
		t.Fatalf("startSession: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body Session
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != "opaque-id" {
		t.Errorf("id = %q, want opaque-id (not the verifier transaction_id)", body.ID)
	}
	if body.WalletLink != fake.startSession.WalletLink {
		t.Errorf("walletLink = %q, want %q", body.WalletLink, fake.startSession.WalletLink)
	}
	if !fake.started {
		t.Fatal("StartPresentation was not called")
	}
}

func TestStartSessionWrapsVerifierError(t *testing.T) {
	h := newTestHandler(&fakeVerifier{startErr: errors.New("verifier down")}, &fakePresentations{})

	err := h.startSession(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/session", nil))
	if err == nil {
		t.Fatal("expected error when the verifier rejects the start")
	}
}

func TestStatusReturnsPresentationStatus(t *testing.T) {
	h := newTestHandler(&fakeVerifier{statusVal: "PENDING"}, &fakePresentations{txID: "tx-abc"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/session/tx/status", nil)
	req.SetPathValue("id", "tx")
	if err := h.status(rec, req); err != nil {
		t.Fatalf("status: %v", err)
	}

	var body statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "PENDING" {
		t.Errorf("status = %q, want PENDING", body.Status)
	}
}

func TestStatusUnknownSessionReportsPending(t *testing.T) {
	// An id we never minted (or one that expired) must not error out: it is
	// indistinguishable from a not-yet-finished presentation, so report PENDING.
	fake := &fakeVerifier{statusVal: "DONE"} // would be DONE if consulted
	h := newTestHandler(fake, &fakePresentations{lookupErr: presentationstore.ErrNotFound})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/session/nope/status", nil)
	req.SetPathValue("id", "nope")
	if err := h.status(rec, req); err != nil {
		t.Fatalf("status: %v", err)
	}

	var body statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != openid4vpverifier.StatusPending {
		t.Errorf("status = %q, want PENDING", body.Status)
	}
}

func TestClaimUnknownSessionIsNotFinished(t *testing.T) {
	// A claim bearing an unknown/expired id must be rejected as not-yet-finished
	// (409), never resolved against the verifier.
	h := newTestHandler(&fakeVerifier{}, &fakePresentations{lookupErr: presentationstore.ErrNotFound})

	req := httptest.NewRequest(http.MethodPost, "/auth/session/nope/claim", nil)
	req.SetPathValue("id", "nope")
	err := h.claim(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "session_not_finished" {
		t.Fatalf("got status=%d code=%q, want 409/session_not_finished", apiErr.Status, apiErr.Code)
	}
}

func TestStatusPassesTransportErrorThrough(t *testing.T) {
	// A transport failure must surface as a generic error (500 via HandlerFunc),
	// not be swallowed or turned into a client error.
	h := newTestHandler(&fakeVerifier{statusErr: errors.New("connection refused")}, &fakePresentations{txID: "tx-abc"})

	req := httptest.NewRequest(http.MethodGet, "/auth/session/tx/status", nil)
	req.SetPathValue("id", "tx")
	if err := h.status(httptest.NewRecorder(), req); err == nil {
		t.Fatal("expected an error for a transport failure")
	}
}
