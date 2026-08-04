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

func TestStoreGetRoundTrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	created := makeOrg(t, pool, "Acme", "acme")

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
	if created.KVKNumber != "kvk-acme" || created.Status != organization.StatusActive {
		t.Errorf("org wallet fields = %+v, want kvk-acme/active", created)
	}
}

func TestStoreUpdateRenamesOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	created := makeOrg(t, pool, "Acme", "acme")

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

	created := makeOrg(t, pool, "Acme", "acme")

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

	org := makeOrg(t, pool, "Acme", "acme")

	_, err := store.GetMembership(ctx, uuid.New(), org.ID)
	if !errors.Is(err, organization.ErrNotMember) {
		t.Errorf("err = %v, want ErrNotMember", err)
	}
}

func TestStoreMembershipsReflectInsertedRows(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
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

func TestStoreListResolvesThemeLogoURI(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	withLogo := makeOrg(t, pool, "Has Logo", "has-logo")
	noLogo := makeOrg(t, pool, "No Logo", "no-logo")

	// A theme row with logo bytes -> the list must resolve a versioned logo path.
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_theme_settings (organization_id, logo_bytes, logo_content_type)
		 VALUES ($1, $2, $3)`,
		withLogo.ID, []byte{0x89, 0x50, 0x4e, 0x47}, "image/png",
	); err != nil {
		t.Fatalf("insert theme logo: %v", err)
	}
	// A theme row without logo bytes must not produce a logo path.
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_theme_settings (organization_id, primary_color) VALUES ($1, $2)`,
		noLogo.ID, "#1d4e89",
	); err != nil {
		t.Fatalf("insert theme colours: %v", err)
	}

	orgs, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[uuid.UUID]organization.Organization{}
	for _, o := range orgs {
		byID[o.ID] = o
	}

	got := byID[withLogo.ID].LogoURI
	wantPrefix := "/api/v1/orgs/has-logo/theme/logo?v="
	if len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Errorf("List LogoURI = %q, want prefix %q with a version", got, wantPrefix)
	}
	if uri := byID[noLogo.ID].LogoURI; uri != "" {
		t.Errorf("List LogoURI for org without a logo = %q, want empty", uri)
	}
}

func TestStoreListForUserResolvesThemeLogoURI(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"alice@example.test", "Test", "User",
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, $3)",
		userID, org.ID, organization.RoleAdmin,
	); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_theme_settings (organization_id, logo_bytes, logo_content_type)
		 VALUES ($1, $2, $3)`,
		org.ID, []byte{0x89, 0x50, 0x4e, 0x47}, "image/png",
	); err != nil {
		t.Fatalf("insert theme logo: %v", err)
	}

	orgs, err := store.ListForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("ListForUser returned %d orgs, want 1", len(orgs))
	}
	wantPrefix := "/api/v1/orgs/acme/theme/logo?v="
	if got := orgs[0].LogoURI; len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Errorf("ListForUser LogoURI = %q, want prefix %q with a version", got, wantPrefix)
	}
}
