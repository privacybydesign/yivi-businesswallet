//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// A session the org's software cancelled says so, and one the person declined
// on the hosted page says that.
func TestProofingAPICancelledBy(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")

	type session struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		HostedURL   string `json:"hostedUrl"`
		CancelledBy string `json:"cancelledBy"`
	}
	mailed := decodeJSON[session](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "bob@example.org", "sendMail": false}))
	if got := decodeJSON[session](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions/"+mailed.ID+"/cancel", key.Secret, nil)); got.CancelledBy != "api_key" {
		t.Errorf("cancelled by the key = %+v, want cancelledBy api_key", got)
	}

	hosted := decodeJSON[session](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true}))
	token := hosted.HostedURL[strings.LastIndex(hosted.HostedURL, "/")+1:]
	if resp := env.postJSON("/api/v1/proof/"+token+"/decline", map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("decline = %d, want 200", resp.StatusCode)
	}
	if got := decodeJSON[session](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+hosted.ID, key.Secret, nil)); got.CancelledBy != "subject" {
		t.Errorf("declined on the page = %+v, want cancelledBy subject", got)
	}
}
