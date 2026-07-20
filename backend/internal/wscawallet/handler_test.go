package wscawallet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

type fakeActivator struct {
	configured                        bool
	account                           wsca.Account
	activateErr, rotateErr, statusErr error
	activateCalled, rotateCalled      bool
}

func (f *fakeActivator) Configured() bool { return f.configured }

func (f *fakeActivator) Activate(context.Context, uuid.UUID, string) (wsca.Account, error) {
	f.activateCalled = true
	return f.account, f.activateErr
}

func (f *fakeActivator) Rotate(context.Context, uuid.UUID, string, string) (wsca.Account, error) {
	f.rotateCalled = true
	return f.account, f.rotateErr
}

func (f *fakeActivator) Status(context.Context, uuid.UUID) (wsca.Account, error) {
	return f.account, f.statusErr
}

// call invokes a handler method through the respond adapter so APIError status
// mapping applies, and returns the recorder.
func call(h func(http.ResponseWriter, *http.Request) error, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/wsca", strings.NewReader(body))
	req = req.WithContext(organization.ContextWithOrg(req.Context(), organization.Organization{ID: uuid.New(), Slug: "acme"}))
	rec := httptest.NewRecorder()
	respond.HandlerFunc(h).ServeHTTP(rec, req)
	return rec
}

func TestActivateHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		fake     *fakeActivator
		wantCode int
	}{
		{"short secret rejected", `{"secret":"123"}`, &fakeActivator{configured: true}, http.StatusBadRequest},
		{"non-digit secret rejected", `{"secret":"abcde"}`, &fakeActivator{configured: true}, http.StatusBadRequest},
		{"malformed body rejected", `{`, &fakeActivator{configured: true}, http.StatusBadRequest},
		{"already activated is a conflict", `{"secret":"123456"}`, &fakeActivator{configured: true, activateErr: ErrAlreadyActivated}, http.StatusConflict},
		{"unconfigured is 503", `{"secret":"123456"}`, &fakeActivator{activateErr: wsca.ErrNotConfigured}, http.StatusServiceUnavailable},
		{"success", `{"secret":"123456"}`, &fakeActivator{configured: true, account: wsca.Account{AccountID: "cert-1"}}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHandler(tc.fake, nil, nil)
			rec := call(h.activate, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestActivateHandlerRejectsBeforeCallingActivator(t *testing.T) {
	t.Parallel()
	fake := &fakeActivator{configured: true}
	h := NewHandler(fake, nil, nil)
	if rec := call(h.activate, `{"secret":"12"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if fake.activateCalled {
		t.Error("activator must not be called for an invalid secret")
	}
}

func TestRotateHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		fake     *fakeActivator
		wantCode int
	}{
		{"missing current secret", `{"newSecret":"123456"}`, &fakeActivator{configured: true}, http.StatusBadRequest},
		{"short new secret", `{"currentSecret":"123456","newSecret":"12"}`, &fakeActivator{configured: true}, http.StatusBadRequest},
		{"not activated is a conflict", `{"currentSecret":"123456","newSecret":"654321"}`, &fakeActivator{configured: true, rotateErr: wsca.ErrNotActivated}, http.StatusConflict},
		{"success", `{"currentSecret":"123456","newSecret":"654321"}`, &fakeActivator{configured: true, account: wsca.Account{AccountID: "cert-1"}}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHandler(tc.fake, nil, nil)
			rec := call(h.rotate, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestStatusHandler(t *testing.T) {
	t.Parallel()
	t.Run("not activated", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&fakeActivator{configured: true, statusErr: wsca.ErrNotActivated}, nil, nil)
		rec := call(h.status, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var v statusView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if !v.Configured || v.Activated || v.Account != nil {
			t.Fatalf("view = %+v, want configured+not-activated", v)
		}
	})
	t.Run("activated", func(t *testing.T) {
		t.Parallel()
		acct := wsca.Account{OrganizationID: uuid.New(), AccountID: "cert-1", CertificateID: "cert-1"}
		h := NewHandler(&fakeActivator{configured: true, account: acct}, nil, nil)
		rec := call(h.status, "")
		var v statusView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if !v.Activated || v.Account == nil || v.Account.AccountID != "cert-1" {
			t.Fatalf("view = %+v, want activated with account", v)
		}
	})
}
