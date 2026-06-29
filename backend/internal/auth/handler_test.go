package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/irmarequestor"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// fakeRequestor is the test double for the irmaRequestor seam, exercising the
// handler without a live daemon.
type fakeRequestor struct {
	startPkg   *irmaserver.SessionPackage
	startErr   error
	statusVal  irma.ServerStatus
	statusErr  error
	resultVal  *irmaserver.SessionResult
	resultErr  error
	gotStartAt *irma.DisclosureRequest
}

func (f *fakeRequestor) StartSession(_ context.Context, req *irma.DisclosureRequest) (*irmaserver.SessionPackage, error) {
	f.gotStartAt = req
	return f.startPkg, f.startErr
}

func (f *fakeRequestor) Result(_ context.Context, _ irma.RequestorToken) (*irmaserver.SessionResult, error) {
	return f.resultVal, f.resultErr
}

func (f *fakeRequestor) Status(_ context.Context, _ irma.RequestorToken) (irma.ServerStatus, error) {
	return f.statusVal, f.statusErr
}

func newTestHandler(r irmaRequestor) *Handler {
	return &Handler{
		svc:    NewService(r, nil, nil, irma.NewAttributeTypeIdentifier(testEmailAttr)),
		cookie: CookieConfig{},
	}
}

func TestStartSessionReturnsDaemonPackage(t *testing.T) {
	fake := &fakeRequestor{
		startPkg: &irmaserver.SessionPackage{
			SessionPtr: &irma.Qr{URL: "https://daemon.example/irma", Type: irma.ActionDisclosing},
			Token:      "tok-abc",
		},
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
	var body irmaserver.SessionPackage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Token != "tok-abc" {
		t.Errorf("token = %q, want tok-abc", body.Token)
	}
	if body.SessionPtr == nil || body.SessionPtr.URL != "https://daemon.example/irma" {
		t.Errorf("sessionPtr not passed through from the daemon package: %+v", body.SessionPtr)
	}
	// The session request must ask for the configured attribute.
	if fake.gotStartAt == nil {
		t.Fatal("StartSession was not called")
	}
}

func TestStartSessionWrapsRequestorError(t *testing.T) {
	h := newTestHandler(&fakeRequestor{startErr: errors.New("daemon down")})

	err := h.startSession(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/session", nil))
	if err == nil {
		t.Fatal("expected error when the daemon rejects the start")
	}
}

func TestStatusMapsUnknownSessionTo404(t *testing.T) {
	h := newTestHandler(&fakeRequestor{statusErr: irmarequestor.ErrUnknownSession})

	req := httptest.NewRequest(http.MethodGet, "/auth/session/tok/status", nil)
	req.SetPathValue("token", "tok")
	err := h.status(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *respond.APIError", err)
	}
	if apiErr.Status != http.StatusNotFound || apiErr.Code != "unknown_session" {
		t.Errorf("got %d/%s, want 404/unknown_session", apiErr.Status, apiErr.Code)
	}
}

func TestStatusPassesTransportErrorThroughAs500(t *testing.T) {
	// A non-sentinel error (transport failure) must NOT become a 404 — it is a
	// real server error, surfaced to the HandlerFunc catch as a generic error.
	h := newTestHandler(&fakeRequestor{statusErr: errors.New("connection refused")})

	req := httptest.NewRequest(http.MethodGet, "/auth/session/tok/status", nil)
	req.SetPathValue("token", "tok")
	err := h.status(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("transport error was mapped to APIError %d/%s; want an opaque error", apiErr.Status, apiErr.Code)
	}
	if err == nil {
		t.Fatal("expected an error for a transport failure")
	}
}

func TestStatusReturnsServerStatus(t *testing.T) {
	h := newTestHandler(&fakeRequestor{statusVal: irma.ServerStatusCancelled})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/session/tok/status", nil)
	req.SetPathValue("token", "tok")
	if err := h.status(rec, req); err != nil {
		t.Fatalf("status: %v", err)
	}

	var body statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != string(irma.ServerStatusCancelled) {
		t.Errorf("status = %q, want %q", body.Status, irma.ServerStatusCancelled)
	}
}
