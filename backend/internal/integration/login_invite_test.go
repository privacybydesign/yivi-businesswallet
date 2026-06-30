//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type pendingInvitesBody struct {
	PendingInvitations []struct {
		ID               uuid.UUID `json:"id"`
		OrganizationSlug string    `json:"organizationSlug"`
	} `json:"pendingInvitations"`
}

// A brand-new invitee (no account) who logs in is routed to their pending
// invitations instead of the dead-end "not invited" error, then accepts by id.
func TestLoginRoutesInvitedNewcomer(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createInvitation(orgID, "newbie@example.test", "New", "Comer")

	env.fake.email = "newbie@example.test" // email-only login disclosure, no account
	resp := env.do(http.MethodPost, "/api/v1/auth/session/test-token/claim", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d, want 200", resp.StatusCode)
	}
	var body pendingInvitesBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.PendingInvitations) != 1 || body.PendingInvitations[0].OrganizationSlug != "acme" {
		t.Fatalf("pendingInvitations = %+v, want one for acme", body.PendingInvitations)
	}

	// Not logged in: no session was minted.
	me := env.do(http.MethodGet, "/api/v1/me", nil)
	_ = me.Body.Close()
	if me.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /me after invited claim = %d, want 401", me.StatusCode)
	}

	// Accept by id with the identity disclosure mints the account + membership.
	env.discloses("newbie@example.test", "New", "Comer")
	acc := env.do(http.MethodPost, "/api/v1/invitations/"+body.PendingInvitations[0].ID.String()+"/accept",
		jsonBody(`{"disclosureToken":"test-token"}`))
	_ = acc.Body.Close()
	if acc.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", acc.StatusCode)
	}
	if n := env.membershipCount(orgID, "newbie@example.test"); n != 1 {
		t.Errorf("membership = %d, want 1", n)
	}
}

func TestLoginNoAccountNoInviteRejected(t *testing.T) {
	env := setup(t)
	env.fake.email = "ghost@example.test"

	resp := env.do(http.MethodPost, "/api/v1/auth/session/test-token/claim", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("claim with no account and no invite = %d, want 403", resp.StatusCode)
	}
}
