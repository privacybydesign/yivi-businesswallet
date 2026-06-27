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
	if resp.StatusCode != http.StatusCreated {
		_ = resp.Body.Close()
		t.Fatalf("POST department = %d, want 201", resp.StatusCode)
	}
	var dept organization.Department
	decode(t, resp, &dept)
	_ = resp.Body.Close()
	if dept.Name != "Engineering" {
		t.Fatalf("created department name = %q, want Engineering", dept.Name)
	}

	resp = env.do(http.MethodPatch, "/api/v1/orgs/acme/departments/"+dept.ID.String(), jsonBody(`{"name":"Platform"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH department = %d, want 200", resp.StatusCode)
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
	var dept organization.Department
	decode(t, resp, &dept)
	_ = resp.Body.Close()

	payload := `{"jobTitle":"CTO","departmentId":"` + dept.ID.String() + `"}`
	resp = env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+admin.ID.String(), jsonBody(payload))
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("PATCH member = %d, want 200", resp.StatusCode)
	}
	var member organization.Member
	decode(t, resp, &member)
	_ = resp.Body.Close()

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
