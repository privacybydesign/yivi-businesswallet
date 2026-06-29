//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// These tests pin the response contract for mutations the frontend does not read
// a body from: they must return a success status with an empty body. A regression
// that starts echoing a resource here would silently reintroduce the schema-drift
// class of bug (backend commits, frontend rejects the unexpected body).

func assertNoBody(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body = %q, want empty", body)
	}
}

func adminEnv(t *testing.T) (*testEnv, uuid.UUID) {
	t.Helper()
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	return env, orgID
}

func TestInviteMemberReturnsCreatedNoBody(t *testing.T) {
	env, _ := adminEnv(t)
	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/members",
		strings.NewReader(`{"email":"new@example.test","givenNames":"New","lastName":"Hire"}`))
	assertNoBody(t, resp, http.StatusCreated)
}

func TestRenameOrgReturnsNoContent(t *testing.T) {
	env, _ := adminEnv(t)
	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme",
		strings.NewReader(`{"name":"Acme Renamed"}`))
	assertNoBody(t, resp, http.StatusNoContent)
}

func TestUpdateMemberReturnsNoContent(t *testing.T) {
	env, orgID := adminEnv(t)
	other := env.createUser("member@example.test")
	env.addMembership(other, orgID, organization.RoleMember)

	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+other.String(),
		strings.NewReader(`{"role":"admin"}`))
	assertNoBody(t, resp, http.StatusNoContent)
}

func TestCreateDepartmentReturnsCreatedNoBody(t *testing.T) {
	env, _ := adminEnv(t)
	resp := env.do(http.MethodPost, "/api/v1/orgs/acme/departments",
		strings.NewReader(`{"name":"Engineering"}`))
	assertNoBody(t, resp, http.StatusCreated)
}

func TestUpdateDepartmentReturnsNoContent(t *testing.T) {
	env, orgID := adminEnv(t)
	var deptID uuid.UUID
	if err := env.pool.QueryRow(context.Background(),
		"INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id",
		orgID, "Engineering").Scan(&deptID); err != nil {
		t.Fatalf("insert department: %v", err)
	}

	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme/departments/"+deptID.String(),
		strings.NewReader(`{"name":"Platform"}`))
	assertNoBody(t, resp, http.StatusNoContent)
}
