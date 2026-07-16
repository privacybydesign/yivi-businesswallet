//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func createUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Test", "User",
	).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func addMembership(t *testing.T, pool *pgxpool.Pool, userID, orgID uuid.UUID, deptID *uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"INSERT INTO memberships (user_id, organization_id, role, department_id) VALUES ($1, $2, $3, $4)",
		userID, orgID, organization.RoleMember, deptID,
	); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// makeOrg inserts a demo organization/business wallet (KVK identity columns are
// required) and returns it.
func makeOrg(t *testing.T, pool *pgxpool.Pool, name, slug string) organization.Organization {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address) VALUES ($1, $2, $3, $4, $5)`,
		name, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost"); err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("get org %q: %v", slug, err)
	}
	return org
}

func TestStoreDepartmentCRUD(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")

	dept, err := store.CreateDepartment(ctx, org.ID, "Engineering")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	if dept.Name != "Engineering" || dept.OrganizationID != org.ID {
		t.Errorf("dept = %+v, want Engineering in org %s", dept, org.ID)
	}

	if _, err := store.CreateDepartment(ctx, org.ID, "Engineering"); !errors.Is(err, organization.ErrDepartmentNameTaken) {
		t.Errorf("duplicate err = %v, want ErrDepartmentNameTaken", err)
	}

	depts, err := store.ListDepartments(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(depts) != 1 || depts[0].ID != dept.ID {
		t.Errorf("ListDepartments = %+v, want [%s]", depts, dept.ID)
	}

	renamed, err := store.UpdateDepartment(ctx, org.ID, dept.ID, "Platform")
	if err != nil {
		t.Fatalf("UpdateDepartment: %v", err)
	}
	if renamed.Name != "Platform" {
		t.Errorf("renamed = %q, want Platform", renamed.Name)
	}

	if err := store.DeleteDepartment(ctx, org.ID, dept.ID); err != nil {
		t.Fatalf("DeleteDepartment: %v", err)
	}
	depts, err = store.ListDepartments(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListDepartments after delete: %v", err)
	}
	if len(depts) != 0 {
		t.Errorf("after delete, departments = %+v, want empty", depts)
	}
}

func TestStoreDepartmentNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")

	if _, err := store.UpdateDepartment(ctx, org.ID, uuid.New(), "Nope"); !errors.Is(err, organization.ErrDepartmentNotFound) {
		t.Errorf("update missing err = %v, want ErrDepartmentNotFound", err)
	}
	if err := store.DeleteDepartment(ctx, org.ID, uuid.New()); !errors.Is(err, organization.ErrDepartmentNotFound) {
		t.Errorf("delete missing err = %v, want ErrDepartmentNotFound", err)
	}
}

func TestStoreUpdateMembership(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	dept, err := store.CreateDepartment(ctx, org.ID, "Engineering")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	userID := createUser(t, pool, "alice@example.test")
	addMembership(t, pool, userID, org.ID, nil)

	title := "Engineer"
	member, err := store.UpdateMembership(ctx, org.ID, userID, nil, &title, &dept.ID)
	if err != nil {
		t.Fatalf("UpdateMembership: %v", err)
	}
	if member.JobTitle == nil || *member.JobTitle != "Engineer" {
		t.Errorf("jobTitle = %v, want Engineer", member.JobTitle)
	}
	if member.DepartmentID == nil || *member.DepartmentID != dept.ID {
		t.Errorf("departmentId = %v, want %s", member.DepartmentID, dept.ID)
	}
	if member.DepartmentName == nil || *member.DepartmentName != "Engineering" {
		t.Errorf("departmentName = %v, want Engineering", member.DepartmentName)
	}

	cleared, err := store.UpdateMembership(ctx, org.ID, userID, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateMembership clear: %v", err)
	}
	if cleared.JobTitle != nil || cleared.DepartmentID != nil || cleared.DepartmentName != nil {
		t.Errorf("cleared = %+v, want job title and department nil", cleared)
	}
}

func TestStoreUpdateMembershipUnknownDepartment(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	userID := createUser(t, pool, "alice@example.test")
	addMembership(t, pool, userID, org.ID, nil)

	unknown := uuid.New()
	if _, err := store.UpdateMembership(ctx, org.ID, userID, nil, nil, &unknown); !errors.Is(err, organization.ErrDepartmentNotFound) {
		t.Errorf("err = %v, want ErrDepartmentNotFound", err)
	}
}

func TestStoreUpdateMembershipRejectsForeignOrgDepartment(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	orgA := makeOrg(t, pool, "A", "a")
	orgB := makeOrg(t, pool, "B", "b")
	deptB, err := store.CreateDepartment(ctx, orgB.ID, "Sales")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	userID := createUser(t, pool, "alice@example.test")
	addMembership(t, pool, userID, orgA.ID, nil)

	if _, err := store.UpdateMembership(ctx, orgA.ID, userID, nil, nil, &deptB.ID); !errors.Is(err, organization.ErrDepartmentNotFound) {
		t.Errorf("cross-org dept err = %v, want ErrDepartmentNotFound", err)
	}
}

func TestStoreDeleteDepartmentInUse(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	dept, err := store.CreateDepartment(ctx, org.ID, "Engineering")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	userID := createUser(t, pool, "alice@example.test")
	addMembership(t, pool, userID, org.ID, &dept.ID)

	if err := store.DeleteDepartment(ctx, org.ID, dept.ID); !errors.Is(err, organization.ErrDepartmentInUse) {
		t.Errorf("err = %v, want ErrDepartmentInUse", err)
	}
}

// Deleting an org must succeed even when a member is assigned to one of its
// departments — the composite FK is NO ACTION, not RESTRICT, so the cascading
// department and membership deletes don't collide.
func TestStoreOrgDeleteCascadesWithAssignedDepartment(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	dept, err := store.CreateDepartment(ctx, org.ID, "Engineering")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	userID := createUser(t, pool, "alice@example.test")
	addMembership(t, pool, userID, org.ID, &dept.ID)

	if _, err := pool.Exec(ctx, "DELETE FROM organizations WHERE id = $1", org.ID); err != nil {
		t.Fatalf("delete org with assigned department: %v", err)
	}
}
