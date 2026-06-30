//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

type failingRecorder struct{}

func (failingRecorder) Record(context.Context, database.Querier, string, audit.Target, map[string]any) error {
	return errors.New("audit boom")
}

func TestStoreCreateRoundTrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	created, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bySlug, err := store.GetBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if bySlug != created {
		t.Errorf("GetBySlug = %+v, want %+v", bySlug, created)
	}

	byID, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID != created {
		t.Errorf("GetByID = %+v, want %+v", byID, created)
	}
}

func TestStoreUpdateRenamesOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	created, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, created.ID, "Acme Renamed")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Acme Renamed" {
		t.Errorf("Name = %q, want %q", updated.Name, "Acme Renamed")
	}
	if updated.Slug != created.Slug {
		t.Errorf("Slug = %q, want %q", updated.Slug, created.Slug)
	}
}

func TestStoreUpdateNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})

	_, err := store.Update(context.Background(), uuid.New(), "Ghost")
	if !errors.Is(err, organization.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStoreUpdateRollsBackWhenAuditFails(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := organization.NewStore(pool, audit.NopRecorder{})

	created, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failing := organization.NewStore(pool, failingRecorder{})
	if _, err := failing.Update(ctx, created.ID, "Acme Renamed"); err == nil {
		t.Fatal("Update succeeded, want error from failing audit")
	}

	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Acme" {
		t.Errorf("Name = %q, want Acme (update must roll back when audit fails)", got.Name)
	}
}

func TestStoreCreateDuplicateSlug(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	if _, err := store.Create(ctx, "Acme", "acme"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := store.Create(ctx, "Acme Two", "acme")
	if !errors.Is(err, organization.ErrSlugTaken) {
		t.Errorf("err = %v, want ErrSlugTaken", err)
	}
}

func TestStoreGetBySlugNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})

	_, err := store.GetBySlug(context.Background(), "ghost")
	if !errors.Is(err, organization.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStoreGetMembershipNotMember(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.GetMembership(ctx, uuid.New(), org.ID)
	if !errors.Is(err, organization.ErrNotMember) {
		t.Errorf("err = %v, want ErrNotMember", err)
	}
}

func TestStoreMembershipsReflectInsertedRows(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const userEmail = "alice@example.test"
	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		userEmail, "Test", "User",
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := pool.Exec(ctx,
		"INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, $3)",
		userID, org.ID, organization.RoleAdmin,
	); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	membership, err := store.GetMembership(ctx, userID, org.ID)
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}
	if membership.Role != organization.RoleAdmin {
		t.Errorf("role = %q, want %q", membership.Role, organization.RoleAdmin)
	}

	orgs, err := store.ListForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != org.ID {
		t.Errorf("ListForUser = %+v, want [%s]", orgs, org.ID)
	}

	members, err := store.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 1 || members[0].UserID != userID || members[0].Email != userEmail {
		t.Errorf("ListMembers = %+v, want one member %s/%s", members, userID, userEmail)
	}
}
