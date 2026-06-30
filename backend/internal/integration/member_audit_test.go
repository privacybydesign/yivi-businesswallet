//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type auditPage struct {
	Events []struct {
		Action     string `json:"action"`
		TargetType string `json:"targetType"`
		TargetID   string `json:"targetId"`
	} `json:"events"`
	NextCursor *string `json:"nextCursor"`
}

func (e *testEnv) memberAuditEvents(slug string, userID uuid.UUID) auditPage {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/orgs/"+slug+"/members/"+userID.String()+"/audit-events", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET member audit-events = %d, want 200", resp.StatusCode)
	}
	var page auditPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		e.t.Fatalf("decode member audit-events: %v", err)
	}
	return page
}

// A member's timeline must span both target_id shapes: the email-keyed
// invitation and the user-id-keyed role change, while excluding other members.
func TestMemberAuditTimelineSpansEmailAndUserID(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"newhire@example.test","givenNames":"New","lastName":"Hire"}`)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite = %d, want 201", resp.StatusCode)
	}

	userID := env.activeMember(orgID, "newhire@example.test", organization.RoleMember, nil, nil)
	if r := env.updateMember("acme", userID, `{"role":"admin"}`); r.StatusCode != http.StatusNoContent {
		_ = r.Body.Close()
		t.Fatalf("promote = %d, want 204", r.StatusCode)
	} else {
		_ = r.Body.Close()
	}

	other := env.activeMember(orgID, "other@example.test", organization.RoleMember, nil, nil)
	if r := env.updateMember("acme", other, `{"role":"admin"}`); r.StatusCode != http.StatusNoContent {
		_ = r.Body.Close()
		t.Fatalf("promote other = %d, want 204", r.StatusCode)
	} else {
		_ = r.Body.Close()
	}

	page := env.memberAuditEvents("acme", userID)

	gotActions := map[string]string{}
	for _, ev := range page.Events {
		gotActions[ev.Action] = ev.TargetID
	}
	if len(page.Events) != 2 {
		t.Fatalf("events = %d (%v), want 2", len(page.Events), gotActions)
	}
	if got := gotActions[audit.MembershipInvited]; got != "newhire@example.test" {
		t.Errorf("invited target_id = %q, want email", got)
	}
	if got := gotActions[audit.MembershipRoleChanged]; got != userID.String() {
		t.Errorf("role_changed target_id = %q, want %s", got, userID)
	}
	for _, ev := range page.Events {
		if ev.TargetID == other.String() {
			t.Errorf("other member's event leaked into timeline: %+v", ev)
		}
	}
}

func TestMemberAuditRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members/"+me.ID.String()+"/audit-events", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member audit as non-admin = %d, want 403", resp.StatusCode)
	}
}

func TestMemberAuditInvalidUserID(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/members/not-a-uuid/audit-events", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid user id = %d, want 400", resp.StatusCode)
	}
}
