//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// TestAuditorReadsMemberDirectoryMemberCannot exercises the RequirePermission
// seam through a real route: the members list is gated on members:read, which
// the new auditor role holds but a plain member does not. This is the finer-role
// half of the RBAC model — a read-only role that sees more than a member without
// being an admin.
func TestAuditorReadsMemberDirectoryMemberCannot(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	auditor := env.login("auditor@example.test")
	env.addMembership(auditor.ID, orgID, organization.RoleAuditor)
	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auditor list members = %d, want 200", resp.StatusCode)
	}

	member := env.login("member@example.test")
	env.addMembership(member.ID, orgID, organization.RoleMember)
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/members", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member list members = %d, want 403", resp.StatusCode)
	}
}

// TestAdminAssignsFunctionalRole confirms the assignment lifecycle accepts the
// new functional roles: an admin may set a member to attestation_issuer, and the
// change persists.
func TestAdminAssignsFunctionalRole(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, nil, nil)

	resp := env.updateMember("acme", userID, `{"role":"`+organization.RoleAttestationIssuer+`"}`)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("assign attestation_issuer = %d, want 204", resp.StatusCode)
	}

	m := byEmail(env.listMembers("acme", organization.StatusActive), "eng@example.test")
	if m == nil {
		t.Fatal("member not found after role change")
	}
	if m.Role != organization.RoleAttestationIssuer {
		t.Errorf("role = %q, want %q", m.Role, organization.RoleAttestationIssuer)
	}
}

// TestDemoteLastAdminToFunctionalRoleConflict guards against the last-admin
// escape the finer roles would otherwise open: demoting the sole admin to a
// functional role (not just to member) must still be refused, or the org is left
// with no administrator.
func TestDemoteLastAdminToFunctionalRoleConflict(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	boss := env.login("boss@example.test")
	env.addMembership(boss.ID, orgID, organization.RoleAdmin)

	resp := env.updateMember("acme", boss.ID, `{"role":"`+organization.RoleAuditor+`"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("demote last admin to auditor = %d, want 409", resp.StatusCode)
	}

	var role string
	if err := env.pool.QueryRow(context.Background(),
		"SELECT role FROM memberships WHERE user_id = $1 AND organization_id = $2",
		boss.ID, orgID,
	).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != organization.RoleAdmin {
		t.Errorf("role after blocked demotion = %q, want admin", role)
	}
}
