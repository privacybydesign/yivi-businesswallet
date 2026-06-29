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

func (e *testEnv) pendingInvitationID(slug, email string) uuid.UUID {
	e.t.Helper()
	entry := byEmail(e.listMembers(slug, organization.StatusInvited), email)
	if entry == nil || entry.InvitationID == nil {
		e.t.Fatalf("no pending invitation for %q", email)
	}
	return *entry.InvitationID
}

func (e *testEnv) auditCount(orgID uuid.UUID, action string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_events WHERE action = $1 AND organization_id = $2", action, orgID,
	).Scan(&n); err != nil {
		e.t.Fatalf("count audit %q: %v", action, err)
	}
	return n
}

func TestRevokeInvitation(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	resp := env.invite("acme", `{"email":"pending@example.test","givenNames":"Pen","lastName":"Ding"}`)
	_ = resp.Body.Close()
	id := env.pendingInvitationID("acme", "pending@example.test")

	del := env.do(http.MethodDelete, "/api/v1/orgs/acme/invitations/"+id.String(), nil)
	_ = del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", del.StatusCode)
	}

	if got := env.listMembers("acme", organization.StatusInvited); len(got) != 0 {
		t.Errorf("invited after revoke = %d, want 0", len(got))
	}
	if n := env.auditCount(orgID, audit.MembershipInviteRevoked); n != 1 {
		t.Errorf("membership.invite_revoked events = %d, want 1", n)
	}
}

func TestResendInvitation(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	resp := env.invite("acme", `{"email":"pending@example.test","givenNames":"Pen","lastName":"Ding"}`)
	_ = resp.Body.Close()
	id := env.pendingInvitationID("acme", "pending@example.test")

	post := env.do(http.MethodPost, "/api/v1/orgs/acme/invitations/"+id.String()+"/resend", nil)
	_ = post.Body.Close()
	if post.StatusCode != http.StatusNoContent {
		t.Fatalf("resend = %d, want 204", post.StatusCode)
	}

	// The invitation stays pending after a resend.
	if byEmail(env.listMembers("acme", organization.StatusInvited), "pending@example.test") == nil {
		t.Error("invitation gone after resend, want still pending")
	}
	if n := env.auditCount(orgID, audit.MembershipInviteResent); n != 1 {
		t.Errorf("membership.invite_resent events = %d, want 1", n)
	}
}

func TestRevokeUnknownInvitation(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	del := env.do(http.MethodDelete, "/api/v1/orgs/acme/invitations/"+uuid.NewString(), nil)
	_ = del.Body.Close()
	if del.StatusCode != http.StatusNotFound {
		t.Errorf("revoke unknown = %d, want 404", del.StatusCode)
	}
}

func TestRevokeInvitationRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	del := env.do(http.MethodDelete, "/api/v1/orgs/acme/invitations/"+uuid.NewString(), nil)
	_ = del.Body.Close()
	if del.StatusCode != http.StatusForbidden {
		t.Errorf("revoke as member = %d, want 403", del.StatusCode)
	}
}
