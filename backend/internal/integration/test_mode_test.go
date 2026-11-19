//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// The API follows the org's mode: what it creates is a test session while the
// org is in test mode, every answer says so, and it reaches only the sessions
// of the mode the org is in.
func TestProofingAPIFollowsTestMode(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	resp := env.postJSON("/api/v1/orgs/acme/identity-proofing/api-keys", map[string]any{"name": "Backend"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d, want 201", resp.StatusCode)
	}
	key := decodeJSON[proofingAPIKeyResp](t, resp)

	type session struct {
		ID   string `json:"id"`
		Test bool   `json:"test"`
	}
	create := func(email string, test bool) session {
		t.Helper()
		resp := env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
			map[string]any{"email": email, "sendMail": false})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create session = %d, want 201", resp.StatusCode)
		}
		if got := resp.Header.Get("Test-Mode") == "true"; got != test {
			t.Errorf("create %s: Test-Mode header %v, want %v", email, got, test)
		}
		s := decodeJSON[session](t, resp)
		if s.Test != test {
			t.Errorf("create %s: test = %v, want %v", email, s.Test, test)
		}
		return s
	}
	reaches := func(mode string, shown, hidden session) {
		t.Helper()
		resp := env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+shown.ID, key.Secret, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: get shown = %d, want 200", mode, resp.StatusCode)
		}
		resp = env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+hidden.ID, key.Secret, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: get hidden = %d, want 404", mode, resp.StatusCode)
		}
		page := decodeJSON[struct {
			Sessions []session `json:"sessions"`
		}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions", key.Secret, nil))
		if len(page.Sessions) != 1 || page.Sessions[0].ID != shown.ID {
			t.Errorf("%s: listed %+v, want only %s", mode, page.Sessions, shown.ID)
		}
	}

	live := create("anna@example.org", false)
	setOrgTestMode(t, env, orgID, true)
	test := create("bob@example.org", true)
	reaches("test", test, live)
	setOrgTestMode(t, env, orgID, false)
	reaches("live", live, test)
}

func setOrgTestMode(t *testing.T, env *testEnv, orgID uuid.UUID, on bool) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(), `UPDATE organizations SET test_mode = $2 WHERE id = $1`, orgID, on); err != nil {
		t.Fatalf("set test mode: %v", err)
	}
}
