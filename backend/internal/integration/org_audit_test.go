//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type orgAuditPage struct {
	Events []struct {
		Action       string          `json:"action"`
		TargetType   string          `json:"targetType"`
		TargetID     string          `json:"targetId"`
		Metadata     json.RawMessage `json:"metadata"`
		Actor        *struct{}       `json:"actor"`
		DetailHidden bool            `json:"detailHidden"`
	} `json:"events"`
}

func (e *testEnv) orgAuditEvents(slug string) orgAuditPage {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/orgs/"+slug+"/audit-events", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET audit-events = %d, want 200", resp.StatusCode)
	}
	var page orgAuditPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		e.t.Fatalf("decode audit-events: %v", err)
	}
	return page
}

// An admin reads the org log in full; an ordinary member reads only the org's
// configuration events, without who acted, on whom and what changed. An
// invitation (its target is the invitee's email) stays admin-only.
func TestOrgAuditDetailIsAdminOnly(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"newhire@example.test","givenNames":"New","lastName":"Hire"}`)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite = %d, want 201", resp.StatusCode)
	}
	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create department = %d, want 201", resp.StatusCode)
	}

	admin := env.orgAuditEvents("acme")
	invited := 0
	for _, ev := range admin.Events {
		if ev.DetailHidden {
			t.Errorf("admin event %s: detailHidden = true, want false", ev.Action)
		}
		if ev.Action == audit.MembershipInvited {
			invited++
			if ev.Actor == nil {
				t.Errorf("admin event %s: actor withheld, want the inviting admin", ev.Action)
			}
		}
	}
	if invited != 1 {
		t.Fatalf("admin sees %d %s events, want 1", invited, audit.MembershipInvited)
	}

	memberID := env.loginAs("member@example.test")
	env.addMembership(memberID, orgID, organization.RoleMember)

	member := env.orgAuditEvents("acme")
	departments := 0
	for _, ev := range member.Events {
		if ev.Action == audit.DepartmentCreated {
			departments++
		}
		if ev.TargetType == audit.TargetMembership {
			t.Errorf("member sees %s, want membership events admin-only", ev.Action)
		}
		if !ev.DetailHidden || ev.Actor != nil || ev.TargetID != "" || string(ev.Metadata) != `{}` {
			t.Errorf("member event %s: detailHidden=%v actor=%v targetId=%q metadata=%s, want detail withheld",
				ev.Action, ev.DetailHidden, ev.Actor, ev.TargetID, ev.Metadata)
		}
	}
	if departments != 1 {
		t.Errorf("member sees %d %s events, want 1", departments, audit.DepartmentCreated)
	}
}
