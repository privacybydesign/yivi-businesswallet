//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func (e *testEnv) updateMember(slug string, userID uuid.UUID, body string) *http.Response {
	e.t.Helper()
	return e.do(
		http.MethodPatch,
		"/api/v1/orgs/"+slug+"/members/"+userID.String(),
		strings.NewReader(body),
	)
}

// inviteMember invites a member and returns the decoded response body.
func (e *testEnv) inviteMember(slug, body string) memberBody {
	e.t.Helper()
	resp := e.invite(slug, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("invite = %d, want 201", resp.StatusCode)
	}
	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		e.t.Fatalf("decode member: %v", err)
	}
	return m
}

func (e *testEnv) createDepartment(orgID uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		"INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id",
		orgID, name,
	).Scan(&id); err != nil {
		e.t.Fatalf("create department: %v", err)
	}
	return id
}

func TestAdminUpdatesMember(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	member := env.inviteMember("acme", `{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer"}`)
	deptID := env.createDepartment(orgID, "Platform")

	resp := env.updateMember("acme", member.UserID,
		`{"jobTitle":"Engineering Lead","departmentId":"`+deptID.String()+`"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d, want 200", resp.StatusCode)
	}

	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.JobTitle == nil || *m.JobTitle != "Engineering Lead" {
		t.Errorf("jobTitle = %v, want Engineering Lead", m.JobTitle)
	}
	if m.DepartmentID == nil || *m.DepartmentID != deptID {
		t.Errorf("departmentId = %v, want %s", m.DepartmentID, deptID)
	}
}

func TestUpdateMemberClearsFields(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	deptID := env.createDepartment(orgID, "Engineering")
	member := env.inviteMember("acme",
		`{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer","jobTitle":"Dev","departmentId":"`+deptID.String()+`"}`)

	resp := env.updateMember("acme", member.UserID, `{"jobTitle":null,"departmentId":null}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d, want 200", resp.StatusCode)
	}

	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.JobTitle != nil || m.DepartmentID != nil {
		t.Errorf("jobTitle/department = %v/%v, want nil/nil", m.JobTitle, m.DepartmentID)
	}
}

func TestUpdateMemberRequiresAdmin(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.updateMember("acme", me.ID, `{"jobTitle":"Self Promoted"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("update as member = %d, want 403", resp.StatusCode)
	}
}

func TestUpdateUnknownMemberNotFound(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.updateMember("acme", uuid.New(), `{"jobTitle":"x"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("update unknown member = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateMemberUnknownDepartment(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	member := env.inviteMember("acme", `{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer"}`)

	resp := env.updateMember("acme", member.UserID, `{"departmentId":"`+uuid.NewString()+`"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("update unknown department = %d, want 400", resp.StatusCode)
	}
}

func TestAdminPromotesMemberPreservingProfile(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	deptID := env.createDepartment(orgID, "Platform")
	member := env.inviteMember("acme",
		`{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer","jobTitle":"Dev","departmentId":"`+deptID.String()+`"}`)

	resp := env.updateMember("acme", member.UserID,
		`{"role":"admin","jobTitle":"Dev","departmentId":"`+deptID.String()+`"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote = %d, want 200", resp.StatusCode)
	}

	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Role != organization.RoleAdmin {
		t.Errorf("role = %q, want admin", m.Role)
	}
	if m.JobTitle == nil || *m.JobTitle != "Dev" {
		t.Errorf("jobTitle = %v, want Dev (must survive role change)", m.JobTitle)
	}
	if m.DepartmentID == nil || *m.DepartmentID != deptID {
		t.Errorf("departmentId = %v, want %s", m.DepartmentID, deptID)
	}
}

func TestAdminDemotesAdminWithCoAdmin(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	other := env.inviteMember("acme",
		`{"email":"co@example.test","givenNames":"Co","lastName":"Admin","role":"admin"}`)

	resp := env.updateMember("acme", other.UserID, `{"role":"member"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("demote = %d, want 200", resp.StatusCode)
	}

	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Role != organization.RoleMember {
		t.Errorf("role = %q, want member", m.Role)
	}
}

func TestDemoteLastAdminConflict(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	boss := env.login("boss@example.test")
	env.addMembership(boss.ID, orgID, organization.RoleAdmin)

	resp := env.updateMember("acme", boss.ID, `{"role":"member"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("demote last admin = %d, want 409", resp.StatusCode)
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

func TestUpdateMemberInvalidRole(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	member := env.inviteMember("acme", `{"email":"eng@example.test","givenNames":"Engi","lastName":"Neer"}`)

	resp := env.updateMember("acme", member.UserID, `{"role":"superuser"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid role = %d, want 400", resp.StatusCode)
	}
}
