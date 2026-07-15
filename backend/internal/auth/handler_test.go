package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
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

func (f *fakeVerifier) StartPresentation(_ context.Context) (openid4vpverifier.Session, error) {
	f.started = true
	return f.startSession, f.startErr
}

func (f *fakeVerifier) Result(_ context.Context, _ string) (openid4vpverifier.Presentation, error) {
	return f.resultVal, f.resultErr
}

func (f *fakeVerifier) Status(_ context.Context, _ string) (string, error) {
	return f.statusVal, f.statusErr
}

func newTestHandler(v verifier) *Handler {
	return &Handler{
		svc:    NewService(v, nil, nil, nil),
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
		startSession: openid4vpverifier.Session{ID: "tx-abc", WalletLink: "openid4vp://?request_uri=https%3A%2F%2Fv.example"},
	}
	h := newTestHandler(fake)

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
	if body.ID != "tx-abc" {
		t.Errorf("id = %q, want tx-abc", body.ID)
	}
	if body.WalletLink != fake.startSession.WalletLink {
		t.Errorf("walletLink = %q, want %q", body.WalletLink, fake.startSession.WalletLink)
	}
	if !fake.started {
		t.Fatal("StartPresentation was not called")
	}
}

func TestStartSessionWrapsVerifierError(t *testing.T) {
	h := newTestHandler(&fakeVerifier{startErr: errors.New("verifier down")})

	err := h.startSession(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/session", nil))
	if err == nil {
		t.Fatal("expected error when the verifier rejects the start")
	}
}

func TestStatusReturnsPresentationStatus(t *testing.T) {
	h := newTestHandler(&fakeVerifier{statusVal: "PENDING"})

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

func TestStatusPassesTransportErrorThrough(t *testing.T) {
	// A transport failure must surface as a generic error (500 via HandlerFunc),
	// not be swallowed or turned into a client error.
	h := newTestHandler(&fakeVerifier{statusErr: errors.New("connection refused")})

	req := httptest.NewRequest(http.MethodGet, "/auth/session/tx/status", nil)
	req.SetPathValue("id", "tx")
	if err := h.status(httptest.NewRecorder(), req); err == nil {
		t.Fatal("expected an error for a transport failure")
	}
}
