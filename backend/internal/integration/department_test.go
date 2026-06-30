//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func jsonBody(s string) *bytes.Reader {
	return bytes.NewReader([]byte(s))
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func (e *testEnv) departmentByName(slug, name string) organization.Department {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/orgs/"+slug+"/departments", nil)
	defer func() { _ = resp.Body.Close() }()
	var depts []organization.Department
	decode(e.t, resp, &depts)
	for _, d := range depts {
		if d.Name == name {
			return d
		}
	}
	e.t.Fatalf("department %q not found", name)
	return organization.Department{}
}

func TestDepartmentMemberCanList(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/departments", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET departments as member = %d, want 200", resp.StatusCode)
	}
}

func TestDepartmentMemberCannotCreate(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@example.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST department as member = %d, want 403", resp.StatusCode)
	}
}

func TestDepartmentAdminCRUD(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST department = %d, want 201", resp.StatusCode)
	}
	dept := env.departmentByName("acme", "Engineering")

	resp = env.do(http.MethodPatch, "/api/v1/orgs/acme/departments/"+dept.ID.String(), jsonBody(`{"name":"Platform"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH department = %d, want 204", resp.StatusCode)
	}
	if got := env.departmentByName("acme", "Platform"); got.ID != dept.ID {
		t.Errorf("renamed department id = %s, want %s", got.ID, dept.ID)
	}

	resp = env.do(http.MethodDelete, "/api/v1/orgs/acme/departments/"+dept.ID.String(), nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE department = %d, want 204", resp.StatusCode)
	}
}

func TestDepartmentCreateDuplicateConflict(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201", resp.StatusCode)
	}

	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate POST = %d, want 409", resp.StatusCode)
	}
}

func TestUpdateMemberAssignsJobTitleAndDepartment(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("boss@example.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/departments", jsonBody(`{"name":"Engineering"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST department = %d, want 201", resp.StatusCode)
	}
	dept := env.departmentByName("acme", "Engineering")

	payload := `{"jobTitle":"CTO","departmentId":"` + dept.ID.String() + `"}`
	resp = env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+admin.ID.String(), jsonBody(payload))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH member = %d, want 204", resp.StatusCode)
	}

	member := byEmail(env.listMembers("acme", organization.StatusActive), "boss@example.test")
	if member == nil {
		t.Fatal("member not found after update")
	}
	if member.JobTitle == nil || *member.JobTitle != "CTO" {
		t.Errorf("jobTitle = %v, want CTO", member.JobTitle)
	}
	if member.DepartmentName == nil || *member.DepartmentName != "Engineering" {
		t.Errorf("departmentName = %v, want Engineering", member.DepartmentName)
	}
}

func TestUpdateMemberUnknownDepartmentBadRequest(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("boss@example.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	payload := `{"departmentId":"` + uuidNil + `"}`
	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+admin.ID.String(), jsonBody(payload))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH member with unknown department = %d, want 400", resp.StatusCode)
	}
}

const uuidNil = "00000000-0000-0000-0000-000000000001"
