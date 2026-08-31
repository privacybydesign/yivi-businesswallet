//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// loginAs switches the authenticated caller, which env.login cannot do: it
// claims the one shared disclosureToken, and a session's idempotency key is that
// token, so sessions.Mint's ON CONFLICT rotates the first user's session token
// without touching its user_id. Claiming on a presentation id of the caller's own
// is what actually swaps who the client is.
func (e *testEnv) loginAs(email string) uuid.UUID {
	e.t.Helper()
	id := e.createUser(email)
	token := "presentation-" + email
	seedPresentation(e.t, e.pool, token)
	e.fake.email = email

	resp := e.do(http.MethodPost, "/api/v1/auth/session/"+token+"/claim", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("claim as %s = %d, want 200", email, resp.StatusCode)
	}
	return id
}

// claimRepresentation makes userID the org's register-backed legal
// representative, the way wallet bootstrap does from the KVK attestation. It is
// the only basis that may grant a mandate nobody delegated to it.
func (e *testEnv) claimRepresentation(orgID, userID uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO wallet_representations
			(organization_id, kind, given_names, family_name, claimed_by_user_id, claimed_at)
		 VALUES ($1, 'bestuurder', 'Test', 'Boss', $2, now())`,
		orgID, userID,
	); err != nil {
		e.t.Fatalf("claim representation: %v", err)
	}
}

func TestMandateAdminCannotGrantOne(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	// An org admin holds an administrative mandate, not the owner's authority.
	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/mandates",
		jsonBody(`{"type":"administrative","granteeUserId":"`+me.ID.String()+`","scope":"organization"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST mandate as plain admin = %d, want 403", resp.StatusCode)
	}
}

func TestMandateGrantListRevoke(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.claimRepresentation(orgID, me.ID)

	grantee := env.createUser("deputy@example.test")
	env.addMembership(grantee, orgID, organization.RoleMember)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/mandates",
		jsonBody(`{"type":"full","granteeUserId":"`+grantee.String()+`","scope":"organization"}`))
	if resp.StatusCode != http.StatusCreated {
		_ = resp.Body.Close()
		t.Fatalf("POST mandate = %d, want 201", resp.StatusCode)
	}
	var granted organization.Mandate
	decode(t, resp, &granted)
	_ = resp.Body.Close()
	if granted.Status != organization.MandateStatusActive {
		t.Errorf("status = %q, want active", granted.Status)
	}

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/mandates", nil)
	var listed []organization.Mandate
	decode(t, resp, &listed)
	_ = resp.Body.Close()
	if len(listed) != 1 || listed[0].ID != granted.ID {
		t.Fatalf("register = %+v, want the one granted mandate", listed)
	}

	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/mandates/"+granted.ID.String()+"/revoke", nil)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("POST revoke = %d, want 200", resp.StatusCode)
	}
	var revoked []organization.Mandate
	decode(t, resp, &revoked)
	_ = resp.Body.Close()
	if len(revoked) != 1 || revoked[0].Status != organization.MandateStatusRevoked {
		t.Fatalf("revoked = %+v, want the mandate marked revoked", revoked)
	}

	// The register keeps it: a revoked mandate that vanished would hide the
	// revocation.
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/mandates", nil)
	decode(t, resp, &listed)
	_ = resp.Body.Close()
	if len(listed) != 1 || listed[0].Status != organization.MandateStatusRevoked {
		t.Errorf("register after revoke = %+v, want the revoked mandate still listed", listed)
	}
}

func TestMandateRevocationWithdrawsAdminInRealTime(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	boss := env.login("boss@example.test")
	env.addMembership(boss.ID, orgID, organization.RoleAdmin)
	env.claimRepresentation(orgID, boss.ID)

	deputy := env.createUser("deputy@example.test")
	env.addMembership(deputy, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/mandates",
		jsonBody(`{"type":"administrative","granteeUserId":"`+deputy.String()+`","scope":"organization"}`))
	if resp.StatusCode != http.StatusCreated {
		_ = resp.Body.Close()
		t.Fatalf("POST mandate = %d, want 201", resp.StatusCode)
	}
	var granted organization.Mandate
	decode(t, resp, &granted)
	_ = resp.Body.Close()

	// The deputy's membership row says admin and their mandate is active.
	env.loginAs("deputy@example.test")
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET members as mandated admin = %d, want 200", resp.StatusCode)
	}

	env.loginAs("boss@example.test")
	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/mandates/"+granted.ID.String()+"/revoke", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST revoke = %d, want 200", resp.StatusCode)
	}

	// Nothing touched the membership row, and the very next request is refused.
	env.loginAs("deputy@example.test")
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET members after the mandate was revoked = %d, want 403", resp.StatusCode)
	}
}

func TestMandateOrgWithoutMandatesIsUnaffected(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	// The mandate layer is opt-in per organisation: an admin in an org that has
	// never granted one keeps every admin route.
	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET members = %d, want 200", resp.StatusCode)
	}
}
