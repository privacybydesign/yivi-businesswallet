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
