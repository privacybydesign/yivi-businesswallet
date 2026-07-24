package organization

import (
	"strings"
	"testing"
)

// wantRolePermissions pins the full RBAC matrix. It is a second, independent
// spelling of rolePermissions: if either drifts the test fails, so a change to
// a role's grants has to be made deliberately in both places.
var wantRolePermissions = map[string][]string{
	RoleAdmin: {
		"members:read", "members:invite", "members:change_role", "members:revoke", "members:review_identity",
		"attestations:read", "attestations:issue", "attestations:cancel_offer", "attestations:revoke",
		"attestations:manage_templates", "attestations:manage_keys",
		"qerds:read", "qerds:provision_address", "qerds:send",
		"settings:read", "settings:manage_theming", "settings:manage_issuer", "settings:manage_smtp",
		"audit:read",
	},
	RoleMember: {
		"attestations:read", "qerds:read",
	},
	RoleAttestationIssuer: {
		"attestations:read", "attestations:issue", "attestations:cancel_offer", "attestations:revoke",
	},
	RoleQerdsOperator: {
		"qerds:read", "qerds:provision_address", "qerds:send",
	},
	RoleAuditor: {
		"members:read", "attestations:read", "qerds:read", "settings:read", "audit:read",
	},
}

func TestRolePermissionsMatchMatrix(t *testing.T) {
	if len(rolePermissions) != len(wantRolePermissions) {
		t.Fatalf("role count = %d, want %d", len(rolePermissions), len(wantRolePermissions))
	}
	for role, want := range wantRolePermissions {
		got := rolePermissions[role]
		if got == nil {
			t.Errorf("role %q missing from rolePermissions", role)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("role %q: %d permissions, want %d", role, len(got), len(want))
		}
		for _, p := range want {
			if _, ok := got[p]; !ok {
				t.Errorf("role %q: missing permission %q", role, p)
			}
		}
	}
}

// TestNoFunctionalRoleHoldsAxisAPermission is the guardrail the design calls for:
// mandate-granting (mandates:*) and wallet-lifecycle (wallet:*) capabilities are
// gated on Axis A (legal representative / full-mandate holder), so no functional
// role — not even admin — may reach them through the role -> permission map.
func TestNoFunctionalRoleHoldsAxisAPermission(t *testing.T) {
	for role, perms := range rolePermissions {
		for p := range perms {
			resource, _, _ := strings.Cut(p, ":")
			if resource == ResourceMandates || resource == ResourceWallet {
				t.Errorf("role %q holds Axis-A-gated permission %q; must be granted via a mandate, not a role", role, p)
			}
		}
	}
}

// TestMemberCannotReadMemberDirectory locks in the deliberate deviation from the
// illustrative table: reading the member directory stays an admin/auditor
// capability, so the members list endpoints keep today's admin-only behaviour.
func TestMemberCannotReadMemberDirectory(t *testing.T) {
	if HasPermission(RoleMember, ResourceMembers, ActionRead) {
		t.Error("member must not hold members:read")
	}
}

// TestAuditorIsReadOnly asserts the auditor holds only read actions.
func TestAuditorIsReadOnly(t *testing.T) {
	for p := range rolePermissions[RoleAuditor] {
		_, action, _ := strings.Cut(p, ":")
		if action != ActionRead {
			t.Errorf("auditor holds non-read permission %q; the auditor is read-only", p)
		}
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		role     string
		resource string
		action   string
		want     bool
	}{
		{RoleAdmin, ResourceMembers, ActionInvite, true},
		{RoleAdmin, ResourceMandates, ActionGrant, false},
		{RoleAdmin, ResourceWallet, ActionActivate, false},
		{RoleMember, ResourceMembers, ActionRead, false},
		{RoleMember, ResourceAttestations, ActionRead, true},
		{RoleAttestationIssuer, ResourceAttestations, ActionIssue, true},
		{RoleAttestationIssuer, ResourceAttestations, ActionManageKeys, false},
		{RoleQerdsOperator, ResourceQERDS, ActionProvisionAddress, true},
		{RoleQerdsOperator, ResourceMembers, ActionInvite, false},
		{RoleAuditor, ResourceAudit, ActionRead, true},
		{RoleAuditor, ResourceMembers, ActionInvite, false},
		{"", ResourceMembers, ActionRead, false},
		{"nonexistent", ResourceAttestations, ActionRead, false},
	}
	for _, tt := range tests {
		if got := HasPermission(tt.role, tt.resource, tt.action); got != tt.want {
			t.Errorf("HasPermission(%q, %q, %q) = %v, want %v", tt.role, tt.resource, tt.action, got, tt.want)
		}
	}
}

func TestIsAssignableRole(t *testing.T) {
	for _, role := range AssignableRolesList() {
		if !IsAssignableRole(role) {
			t.Errorf("AssignableRolesList contains %q but IsAssignableRole says no", role)
		}
	}
	// Every assignable role must have a permission set, and vice versa: an
	// assignable role with no entry in the matrix would silently hold nothing.
	if len(assignableRoles) != len(rolePermissions) {
		t.Errorf("assignable roles = %d, permission-mapped roles = %d; they must match", len(assignableRoles), len(rolePermissions))
	}
	for role := range assignableRoles {
		if _, ok := rolePermissions[role]; !ok {
			t.Errorf("assignable role %q has no permission set", role)
		}
	}
	if IsAssignableRole("platform_admin") {
		t.Error("platform admin is deployment-level and must not be an assignable org role")
	}
	if IsAssignableRole("") {
		t.Error("empty role must not be assignable")
	}
}
