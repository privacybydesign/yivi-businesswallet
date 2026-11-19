//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// apiErrorCode is the code of the API's error answer.
func apiErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	return decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp).Code
}

// An org building its own page runs a Yivi session with its API key alone: it
// starts the Yivi app and its poll settles the session, with no page of the
// wallet's open. The document photo routes answer only for a started session
// of a flow that takes one, and only for the org's own sessions.
func TestProofingOwnPageHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")

	hosted := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"hosted": true}))
	session := "/api/v1/proofing/sessions/" + hosted.ID

	resp := env.apiCall(http.MethodPost, session+"/document-scan", key.Secret, nil)
	if code := apiErrorCode(t, resp); resp.StatusCode != http.StatusConflict || code != "session_not_started" {
		t.Errorf("scan before the start = %d %s, want 409 session_not_started", resp.StatusCode, code)
	}

	started := decodeJSON[struct {
		WalletLink string `json:"walletLink"`
	}](t, env.apiCall(http.MethodPost, session+"/methods/yivi_app", key.Secret, nil))
	if !strings.HasPrefix(started.WalletLink, "openid4vp://") {
		t.Fatalf("headless Yivi start = %+v; want the Yivi app's link", started)
	}

	// The default flow takes no document photo.
	resp = env.apiCall(http.MethodPost, session+"/document-scan", key.Secret, map[string]any{"language": "nl"})
	if code := apiErrorCode(t, resp); resp.StatusCode != http.StatusConflict || code != "document_scan_not_asked" {
		t.Errorf("scan on a flow without a photo = %d %s, want 409 document_scan_not_asked", resp.StatusCode, code)
	}
	resp = env.apiCall(http.MethodPost, session+"/document-scan/finish", key.Secret,
		map[string]any{"transactionId": "trx-1", "resultId": "res-1"})
	if code := apiErrorCode(t, resp); resp.StatusCode != http.StatusConflict || code != "document_scan_not_asked" {
		t.Errorf("finish on a flow without a photo = %d %s, want 409 document_scan_not_asked", resp.StatusCode, code)
	}

	// The poll collects what the person shared in the Yivi app, and answers
	// the session the engine then settled.
	env.fake.givenNames, env.fake.familyName, env.fake.dateOfBirth = "Anna", "Jansen", "1990-03-10"
	env.proofing.Outcome = proofingprovider.StatusApproved
	read := env.fake.results.Load()
	status := decodeJSON[struct {
		Status string `json:"status"`
		Done   bool   `json:"done"`
	}](t, env.apiCall(http.MethodGet, session+"/methods/yivi_app/status", key.Secret, nil))
	if status.Status != "approved" || !status.Done {
		t.Errorf("poll after the person shared = %+v, want approved and done", status)
	}
	if got := env.fake.results.Load() - read; got != 1 {
		t.Errorf("the poll read the Yivi presentation %d times, want once", got)
	}
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, session, key.Secret, nil)); read.Status != "approved" {
		t.Errorf("session = %+v, want approved", read)
	}

	// Another org's key reaches none of it.
	otherID := env.createOrg("Other", "other")
	env.addMembership(me.ID, otherID, organization.RoleAdmin)
	other := env.proofingKey("other", "Portal")
	for _, path := range []string{session + "/document-scan", session + "/document-scan/finish"} {
		resp := env.apiCall(http.MethodPost, path, other.Secret, map[string]any{})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s with another org's key = %d, want 404", path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}
