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

	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	if m.Email != "newhire@example.test" {
		t.Errorf("email = %q, want lowercased newhire@example.test", m.Email)
	}
	if m.GivenNames != "New" || m.LastName != "Hire" {
		t.Errorf("name = %q %q, want New Hire", m.GivenNames, m.LastName)
	}
	if m.Role != organization.RoleMember {
		t.Errorf("role = %q, want member", m.Role)
	}
	if m.JobTitle != nil || m.DepartmentID != nil {
		t.Errorf("jobTitle/department = %v/%v, want nil/nil", m.JobTitle, m.DepartmentID)
	}
}

func TestInviteReusesExistingUser(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")
	env.adminOf("globex", "Globex", "boss@example.test")

	first := env.invite("acme", `{"email":"shared@example.test","givenNames":"Sam","lastName":"Shared"}`)
	var a memberBody
	_ = json.NewDecoder(first.Body).Decode(&a)
	_ = first.Body.Close()

	second := env.invite("globex", `{"email":"shared@example.test","givenNames":"Different","lastName":"Typed"}`)
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second invite = %d, want 201", second.StatusCode)
	}
	var b memberBody
	if err := json.NewDecoder(second.Body).Decode(&b); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	if b.UserID != a.UserID {
		t.Errorf("reused userId = %s, want %s", b.UserID, a.UserID)
	}
	// The existing user keeps their stored name; the second invite's typed name is ignored.
	if b.GivenNames != "Sam" {
		t.Errorf("givenNames = %q, want Sam (existing name preserved)", b.GivenNames)
	}
}

func TestInviteAlreadyMemberConflict(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	first := env.invite("acme", `{"email":"dup@example.test","givenNames":"Dup","lastName":"Licate"}`)
	_ = first.Body.Close()

	second := env.invite("acme", `{"email":"dup@example.test","givenNames":"Dup","lastName":"Licate"}`)
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("re-invite = %d, want 409", second.StatusCode)
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite with department = %d, want 201", resp.StatusCode)
	}
	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	if m.JobTitle == nil || *m.JobTitle != "Developer" {
		t.Errorf("jobTitle = %v, want Developer", m.JobTitle)
	}
	if m.DepartmentID == nil || *m.DepartmentID != deptID {
		t.Errorf("departmentId = %v, want %s", m.DepartmentID, deptID)
	}
}

func TestInviteAsAdminRole(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"co@example.test","givenNames":"Co","lastName":"Admin","role":"admin"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite admin = %d, want 201", resp.StatusCode)
	}
	var m memberBody
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	if m.Role != organization.RoleAdmin {
		t.Errorf("role = %q, want admin", m.Role)
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
