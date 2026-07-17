//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func (e *testEnv) offboardMember(slug string, userID uuid.UUID) *http.Response {
	e.t.Helper()
	return e.do(http.MethodDelete, "/api/v1/orgs/"+slug+"/members/"+userID.String(), nil)
}

func TestAdminOffboardsMember(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, nil, nil)

	resp := env.offboardMember("acme", userID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("offboard = %d, want 204", resp.StatusCode)
	}

	if m := byEmail(env.listMembers("acme", organization.StatusActive), "eng@example.test"); m != nil {
		t.Errorf("member still listed after off-board: %+v", m)
	}

	// The removal is recorded as a membership.revoked audit event targeting the
	// off-boarded user, so the org timeline retains the action.
	var email string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT metadata->'before'->>'email' FROM audit_events
		 WHERE action = $1 AND organization_id = $2 AND target_id = $3`,
		audit.MembershipRevoked, orgID, userID.String(),
	).Scan(&email); err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if email != "eng@example.test" {
		t.Errorf("audit before.email = %q, want eng@example.test", email)
	}
}

func TestOffboardMemberWithCoAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	other := env.activeMember(orgID, "co@example.test", organization.RoleAdmin, nil, nil)

	resp := env.offboardMember("acme", other)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("offboard co-admin = %d, want 204", resp.StatusCode)
	}
	if m := byEmail(env.listMembers("acme", organization.StatusActive), "co@example.test"); m != nil {
		t.Errorf("co-admin still listed after off-board: %+v", m)
	}
}

func TestOffboardLastAdminConflict(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	boss := env.login("boss@example.test")
	env.addMembership(boss.ID, orgID, organization.RoleAdmin)

	resp := env.offboardMember("acme", boss.ID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("offboard last admin = %d, want 409", resp.StatusCode)
	}

	var count int
	if err := env.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM memberships WHERE user_id = $1 AND organization_id = $2",
		boss.ID, orgID,
	).Scan(&count); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if count != 1 {
		t.Errorf("membership count after blocked off-board = %d, want 1 (last admin must stay)", count)
	}
}

func TestOffboardRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.offboardMember("acme", me.ID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("offboard as member = %d, want 403", resp.StatusCode)
	}
}

func TestOffboardUnknownMemberNotFound(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.offboardMember("acme", uuid.New())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("offboard unknown member = %d, want 404", resp.StatusCode)
	}
}
