//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

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

func TestMembersListReturnsPagedShape(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members?sort=name&dir=desc", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET members = %d, want 200", resp.StatusCode)
	}

	var page struct {
		Entries []struct {
			Status string  `json:"status"`
			UserID *string `json:"userId"`
			Email  string  `json:"email"`
		} `json:"entries"`
		Total int `json:"total"`
	}
	decode(t, resp, &page)
	if page.Total != 1 || len(page.Entries) != 1 {
		t.Fatalf("page = %d entries / total %d, want 1 / 1", len(page.Entries), page.Total)
	}
	if page.Entries[0].Email != "boss@example.test" {
		t.Errorf("entry email = %q, want boss@example.test", page.Entries[0].Email)
	}
}

func TestMembersListRejectsInvalidSort(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members?sort=bogus", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET members?sort=bogus = %d, want 400", resp.StatusCode)
	}
}

func TestGetSingleMember(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members/"+me.ID.String(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET member = %d, want 200", resp.StatusCode)
	}
	var member struct {
		UserID string `json:"userId"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	decode(t, resp, &member)
	if member.UserID != me.ID.String() || member.Role != organization.RoleAdmin {
		t.Errorf("member = %+v, want %s/admin", member, me.ID)
	}

	missing := env.do(http.MethodGet, "/api/v1/orgs/acme/members/"+uuid.NewString(), nil)
	_ = missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("GET unknown member = %d, want 404", missing.StatusCode)
	}
}
