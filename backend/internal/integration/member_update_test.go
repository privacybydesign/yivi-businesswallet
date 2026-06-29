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

type memberBody struct {
	UserID       uuid.UUID  `json:"userId"`
	Email        string     `json:"email"`
	GivenNames   string     `json:"givenNames"`
	LastName     string     `json:"lastName"`
	Role         string     `json:"role"`
	JobTitle     *string    `json:"jobTitle"`
	DepartmentID *uuid.UUID `json:"departmentId"`
}

func (e *testEnv) updateMember(slug string, userID uuid.UUID, body string) *http.Response {
	e.t.Helper()
	return e.do(
		http.MethodPatch,
		"/api/v1/orgs/"+slug+"/members/"+userID.String(),
		strings.NewReader(body),
	)
}

// activeMember provisions a user and an active membership directly, so the
// update tests have a member to PATCH (the invite endpoint only yields a
// pending invitation now).
func (e *testEnv) activeMember(orgID uuid.UUID, email, role string, jobTitle *string, deptID *uuid.UUID) uuid.UUID {
	e.t.Helper()
	userID := e.createUser(email)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO memberships (user_id, organization_id, role, job_title, department_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, orgID, role, jobTitle, deptID,
	); err != nil {
		e.t.Fatalf("active member %q: %v", email, err)
	}
	return userID
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
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, nil, nil)
	deptID := env.createDepartment(orgID, "Platform")

	resp := env.updateMember("acme", userID,
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
	jobTitle := "Dev"
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, &jobTitle, &deptID)

	resp := env.updateMember("acme", userID, `{"jobTitle":null,"departmentId":null}`)
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
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, nil, nil)

	resp := env.updateMember("acme", userID, `{"departmentId":"`+uuid.NewString()+`"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("update unknown department = %d, want 400", resp.StatusCode)
	}
}

func TestAdminPromotesMemberPreservingProfile(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	deptID := env.createDepartment(orgID, "Platform")
	jobTitle := "Dev"
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, &jobTitle, &deptID)

	resp := env.updateMember("acme", userID,
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
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	other := env.activeMember(orgID, "co@example.test", organization.RoleAdmin, nil, nil)

	resp := env.updateMember("acme", other, `{"role":"member"}`)
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
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	userID := env.activeMember(orgID, "eng@example.test", organization.RoleMember, nil, nil)

	resp := env.updateMember("acme", userID, `{"role":"superuser"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid role = %d, want 400", resp.StatusCode)
	}
}
