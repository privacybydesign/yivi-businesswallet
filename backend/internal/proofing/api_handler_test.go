package proofing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// An integrator starts a session only in a method it can finish over the API:
// pending, the hosted page's own, is refused before anything starts.
func TestAPIStartMethodRefusesPending(t *testing.T) {
	for _, method := range []string{"pending", "fax"} {
		r := httptest.NewRequest(http.MethodPost, "/proofing/sessions/x/methods/"+method, nil)
		r.SetPathValue("sessionID", publicSessionID(uuid.New()))
		r.SetPathValue("method", method)
		w := httptest.NewRecorder()
		h := &Handler{}
		var answer *respond.APIError
		if err := h.apiStartMethod(w, r); !errors.As(err, &answer) || answer.Status != http.StatusBadRequest || answer.Code != "invalid_input" {
			t.Errorf("start %s = %v, want 400 invalid_input", method, err)
		}
	}
}
