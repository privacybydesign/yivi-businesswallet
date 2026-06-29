//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func (e *testEnv) invite(slug, body string) *http.Response {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/v1/orgs/"+slug+"/members", strings.NewReader(body))
}

func (e *testEnv) adminOf(slug, name, email string) uuid.UUID {
	e.t.Helper()
	orgID := e.createOrg(name, slug)
	me := e.login(email)
	e.addMembership(me.ID, orgID, organization.RoleAdmin)
	return orgID
}

func TestAdminInvitesNewMember(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"NewHire@Example.test","givenNames":"New","lastName":"Hire"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite = %d, want 201", resp.StatusCode)
	}

	// No users row is created at invite time — the user is minted on accept.
	var users int
	if err := env.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE email = $1", "newhire@example.test").Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Errorf("users for invitee = %d, want 0 (no shell on invite)", users)
	}

	pending := byEmail(env.listMembers("acme", organization.StatusInvited), "newhire@example.test")
	if pending == nil {
		t.Fatal("invited entry missing (email should be lowercased)")
	}
	if pending.GivenNames != "New" || pending.LastName != "Hire" {
		t.Errorf("name = %q %q, want New Hire", pending.GivenNames, pending.LastName)
	}
	if pending.Role != organization.RoleMember {
		t.Errorf("role = %q, want member", pending.Role)
	}
}

func TestInviteSameEmailDifferentOrgs(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	env.adminOf("globex", "Globex", "boss@example.test")

	first := env.invite("acme", `{"email":"shared@example.test","givenNames":"Sam","lastName":"Shared"}`)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first invite = %d, want 201", first.StatusCode)
	}
	second := env.invite("globex", `{"email":"shared@example.test","givenNames":"Different","lastName":"Typed"}`)
	_ = second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second invite = %d, want 201", second.StatusCode)
	}

	// Each invitation carries the name that org's admin typed; nothing is shared.
	pending := byEmail(env.listMembers("globex", organization.StatusInvited), "shared@example.test")
	if pending == nil || pending.GivenNames != "Different" {
		t.Errorf("globex invited name = %+v, want Different (per-invitation, not shared)", pending)
	}
}

func TestReInvitePendingConflict(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	first := env.invite("acme", `{"email":"dup@example.test","givenNames":"Dup","lastName":"Licate"}`)
	_ = first.Body.Close()

	second := env.invite("acme", `{"email":"dup@example.test","givenNames":"Dup","lastName":"Licate"}`)
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("re-invite pending = %d, want 409", second.StatusCode)
	}
}

func TestInviteAlreadyActiveMemberConflict(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	alice := env.createUser("alice@example.test")
	env.addMembership(alice, orgID, organization.RoleMember)

	resp := env.invite("acme", `{"email":"alice@example.test","givenNames":"Alice","lastName":"Active"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("invite active member = %d, want 409", resp.StatusCode)
	}
}

func TestInviteRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.invite("acme", `{"email":"newhire@example.test","givenNames":"New","lastName":"Hire"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("invite as member = %d, want 403", resp.StatusCode)
	}
}

func TestInviteValidatesInput(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	cases := map[string]string{
		"missing email":      `{"givenNames":"New","lastName":"Hire"}`,
		"missing given name": `{"email":"x@example.test","lastName":"Hire"}`,
		"missing last name":  `{"email":"x@example.test","givenNames":"New"}`,
		"malformed email":    `{"email":"notanemail","givenNames":"New","lastName":"Hire"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := env.invite("acme", body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("invite %q = %d, want 400", name, resp.StatusCode)
			}
		})
	}
}

func TestInviteWithJobTitleAndDepartment(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	var deptID uuid.UUID
	err := env.pool.QueryRow(context.Background(),
		"INSERT INTO departments (organization_id, name) VALUES ($1, 'Engineering') RETURNING id", orgID,
	).Scan(&deptID)
	if err != nil {
		t.Fatalf("create department: %v", err)
	}

	body := `{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer","jobTitle":"Developer","departmentId":"` + deptID.String() + `"}`
	resp := env.invite("acme", body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite with department = %d, want 201", resp.StatusCode)
	}

	pending := byEmail(env.listMembers("acme", organization.StatusInvited), "eng@example.test")
	if pending == nil {
		t.Fatal("invited entry missing")
	}
	if pending.JobTitle == nil || *pending.JobTitle != "Developer" {
		t.Errorf("jobTitle = %v, want Developer", pending.JobTitle)
	}
	if pending.DepartmentID == nil || *pending.DepartmentID != deptID {
		t.Errorf("departmentId = %v, want %s", pending.DepartmentID, deptID)
	}
}

func TestInviteAsAdminRole(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"co@example.test","givenNames":"Co","lastName":"Admin","role":"admin"}`)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite admin = %d, want 201", resp.StatusCode)
	}

	pending := byEmail(env.listMembers("acme", organization.StatusInvited), "co@example.test")
	if pending == nil || pending.Role != organization.RoleAdmin {
		t.Errorf("invited role = %+v, want admin", pending)
	}
}

func TestInviteInvalidRoleBadRequest(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"x@example.test","givenNames":"New","lastName":"Hire","role":"superuser"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invite invalid role = %d, want 400", resp.StatusCode)
	}
}

func TestInviteUnknownDepartmentBadRequest(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	body := `{"email":"x@example.test","givenNames":"New","lastName":"Hire","departmentId":"` + uuid.NewString() + `"}`
	resp := env.invite("acme", body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invite unknown department = %d, want 400", resp.StatusCode)
	}
}
