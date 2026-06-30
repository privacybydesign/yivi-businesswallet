//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type memberEntry struct {
	Status         string     `json:"status"`
	UserID         *uuid.UUID `json:"userId"`
	InvitationID   *uuid.UUID `json:"invitationId"`
	Email          string     `json:"email"`
	PreferredName  *string    `json:"preferredName"`
	GivenNames     string     `json:"givenNames"`
	LastName       string     `json:"lastName"`
	Role           string     `json:"role"`
	JobTitle       *string    `json:"jobTitle"`
	DepartmentID   *uuid.UUID `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	InvitedBy      *uuid.UUID `json:"invitedBy"`
}

func (e *testEnv) listMembers(slug, status string) []memberEntry {
	e.t.Helper()
	path := "/api/v1/orgs/" + slug + "/members"
	if status != "" {
		path += "?status=" + status
	}
	resp := e.do(http.MethodGet, path, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("list members = %d, want 200", resp.StatusCode)
	}
	var entries []memberEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		e.t.Fatalf("decode member list: %v", err)
	}
	return entries
}

func byEmail(entries []memberEntry, email string) *memberEntry {
	for i := range entries {
		if entries[i].Email == email {
			return &entries[i]
		}
	}
	return nil
}

func TestMemberListUnionActiveAndInvited(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test") // active admin
	resp := env.invite("acme", `{"email":"pending@example.test","givenNames":"Pen","lastName":"Ding"}`)
	_ = resp.Body.Close()

	entries := env.listMembers("acme", "")
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (1 active, 1 invited)", len(entries))
	}

	boss := byEmail(entries, "boss@example.test")
	if boss == nil || boss.Status != organization.StatusActive || boss.UserID == nil {
		t.Errorf("active entry = %+v, want status active with userId", boss)
	}
	pending := byEmail(entries, "pending@example.test")
	if pending == nil || pending.Status != organization.StatusInvited {
		t.Fatalf("invited entry = %+v, want status invited", pending)
	}
	if pending.UserID != nil {
		t.Errorf("invited userId = %v, want nil", pending.UserID)
	}
	if pending.InvitationID == nil || pending.ExpiresAt == nil || pending.InvitedBy == nil {
		t.Errorf("invited entry missing invitationId/expiresAt/invitedBy: %+v", pending)
	}
}

func TestMemberListStatusFilter(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	resp := env.invite("acme", `{"email":"pending@example.test","givenNames":"Pen","lastName":"Ding"}`)
	_ = resp.Body.Close()

	active := env.listMembers("acme", organization.StatusActive)
	if len(active) != 1 || active[0].Status != organization.StatusActive {
		t.Errorf("status=active returned %+v, want 1 active", active)
	}

	invited := env.listMembers("acme", organization.StatusInvited)
	if len(invited) != 1 || invited[0].Status != organization.StatusInvited {
		t.Errorf("status=invited returned %+v, want 1 invited", invited)
	}

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/members?status=bogus", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=bogus = %d, want 400", resp.StatusCode)
	}
}

func TestMemberListPendingShowsInvitedNameNotProfile(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	// A global user exists with profile name "Test User" (createUser default)...
	env.createUser("bob@example.test")
	// ...but the admin invites bob asserting a different name.
	resp := env.invite("acme", `{"email":"bob@example.test","givenNames":"Robert","lastName":"Asserted"}`)
	_ = resp.Body.Close()

	pending := byEmail(env.listMembers("acme", organization.StatusInvited), "bob@example.test")
	if pending == nil {
		t.Fatal("invited entry for bob missing")
	}
	if pending.GivenNames != "Robert" || pending.LastName != "Asserted" {
		t.Errorf("invited name = %q %q, want the admin-typed Robert Asserted (not the profile)", pending.GivenNames, pending.LastName)
	}
	if pending.PreferredName != nil {
		t.Errorf("invited entry leaked profile field: preferred=%v", pending.PreferredName)
	}
}
