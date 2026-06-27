//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func TestOrgMemberCanViewOrg(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /orgs/acme as member = %d, want 200", resp.StatusCode)
	}
}

func TestOrgNonMemberForbidden(t *testing.T) {
	env := setup(t)
	env.createOrg("Acme", "acme")
	env.login("outsider@example.test") // no membership

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /orgs/acme as non-member = %d, want 403", resp.StatusCode)
	}
}

func TestOrgUnknownSlugNotFound(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")

	resp := env.do(http.MethodGet, "/api/v1/orgs/ghost", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /orgs/ghost = %d, want 404", resp.StatusCode)
	}
}

func TestOrgMembersRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /orgs/acme/members as member = %d, want 403", resp.StatusCode)
	}
}

func TestOrgAdminCanListMembers(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /orgs/acme/members as admin = %d, want 200", resp.StatusCode)
	}
}

func TestPlatformAdminBypassesMembership(t *testing.T) {
	const adminEmail = "admin@example.test"
	env := setup(t, adminEmail)
	env.login(adminEmail)

	// Platform admin lists all organizations without any membership.
	resp := env.do(http.MethodGet, "/api/v1/organizations", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /organizations as platform admin = %d, want 200", resp.StatusCode)
	}
}

func TestRegularUserCannotListAllOrgs(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")

	resp := env.do(http.MethodGet, "/api/v1/organizations", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /organizations as regular user = %d, want 403", resp.StatusCode)
	}
}
