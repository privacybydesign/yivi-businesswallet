package proofingengine

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// An access refusal keeps its own status; a store failure is a 500 that does
// not echo its text, and an unknown session a 404.
func TestWriteAccessError(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
	}{
		"handed over":   {session.ErrDeviceHandedOver, http.StatusForbidden},
		"finished":      {errSessionComplete, http.StatusConflict},
		"unknown":       {fmt.Errorf("read: %w", session.ErrNotFound), http.StatusNotFound},
		"store failure": {errors.New("session: read for update: context deadline exceeded"), http.StatusInternalServerError},
	} {
		rec := httptest.NewRecorder()
		writeAccessError(rec, httptest.NewRequest(http.MethodPost, "/", nil), tc.err)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.status)
		}
		if tc.status == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "deadline") {
			t.Errorf("%s: body %q echoes the internal error", name, rec.Body.String())
		}
	}
}

// A refused app request is audited once per session, device and reason: an
// app retrying the same refused call adds no rows, another reason or device
// does.
func TestAccessDeniedAuditedOnce(t *testing.T) {
	trail := &fakeDeviceTrail{}
	s := &Server{cfg: Config{DeviceTrail: trail}}
	sess := session.Session{ID: "ps_1", TenantID: "org"}
	r := httptest.NewRequest(http.MethodGet, "/app/tok", nil)
	for range 3 {
		s.denyApp(httptest.NewRecorder(), r, sess, appCaller{}, session.ErrDeviceUnauthorized)
	}
	s.denyApp(httptest.NewRecorder(), r, sess, appCaller{}, errSessionExpired)
	s.denyApp(httptest.NewRecorder(), r, session.Session{ID: "ps_2", TenantID: "org"}, appCaller{}, session.ErrDeviceUnauthorized)
	if len(trail.events) != 3 {
		t.Fatalf("recorded %d denials, want one per session and reason: %+v", len(trail.events), trail.events)
	}
}
