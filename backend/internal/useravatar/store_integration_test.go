//go:build integration

package useravatar_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/useravatar"
)

func TestGetWithoutStoredAvatar(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	userID := makeUser(t, pool, "nobody@example.test")

	_, err := store.Get(context.Background(), userID)
	if !errors.Is(err, useravatar.ErrNoAvatar) {
		t.Fatalf("Get = %v, want ErrNoAvatar", err)
	}
}

func TestSaveThenGetRoundtripsBytes(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	userID := makeUser(t, pool, "ann@example.test")
	ctx := context.Background()

	want := useravatar.Avatar{Bytes: []byte{0x89, 'P', 'N', 'G', 1, 2, 3}, ContentType: "image/png"}
	savedAt, err := store.Save(ctx, userID, want)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if savedAt.IsZero() {
		t.Error("Save returned a zero updated_at")
	}

	got, err := store.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Bytes) != string(want.Bytes) || got.ContentType != want.ContentType {
		t.Errorf("Get = %+v, want the saved avatar", got)
	}
	if !got.UpdatedAt.Equal(savedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, savedAt)
	}
}

func TestSaveReplacesAndMovesTheVersionForward(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	userID := makeUser(t, pool, "ann@example.test")
	ctx := context.Background()

	first, err := store.Save(ctx, userID, useravatar.Avatar{Bytes: []byte{1}, ContentType: "image/png"})
	if err != nil {
		t.Fatalf("Save first: %v", err)
	}
	second, err := store.Save(ctx, userID, useravatar.Avatar{Bytes: []byte{2, 2}, ContentType: "image/jpeg"})
	if err != nil {
		t.Fatalf("Save second: %v", err)
	}
	if second.Before(first) {
		t.Errorf("second updated_at %v is before the first %v", second, first)
	}

	got, err := store.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Bytes) != 2 || got.ContentType != "image/jpeg" {
		t.Errorf("Get = %+v, want the replacement avatar", got)
	}
}

// Removing an avatar that was never set is the caller getting what they asked
// for, not an error.
func TestDeleteIsIdempotent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	userID := makeUser(t, pool, "ann@example.test")
	ctx := context.Background()

	if err := store.Delete(ctx, userID); err != nil {
		t.Fatalf("Delete with no avatar: %v", err)
	}
	if _, err := store.Save(ctx, userID, useravatar.Avatar{Bytes: []byte{1}, ContentType: "image/png"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, userID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, userID); !errors.Is(err, useravatar.ErrNoAvatar) {
		t.Fatalf("Get after Delete = %v, want ErrNoAvatar", err)
	}
}

// The org-scoped read is what authorises an administrator to see a photo, so it
// must return nothing for someone who is not a member of that organisation.
func TestGetForOrgMemberOnlyServesMembers(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	ctx := context.Background()

	member := makeUser(t, pool, "member@example.test")
	outsider := makeUser(t, pool, "outsider@example.test")
	orgID := makeOrg(t, pool, "acme")
	addMembership(t, pool, orgID, member)

	avatar := useravatar.Avatar{Bytes: []byte{1, 2, 3}, ContentType: "image/png"}
	if _, err := store.Save(ctx, member, avatar); err != nil {
		t.Fatalf("Save member: %v", err)
	}
	if _, err := store.Save(ctx, outsider, avatar); err != nil {
		t.Fatalf("Save outsider: %v", err)
	}

	if _, err := store.GetForOrgMember(ctx, member, orgID); err != nil {
		t.Fatalf("GetForOrgMember member: %v", err)
	}
	if _, err := store.GetForOrgMember(ctx, outsider, orgID); !errors.Is(err, useravatar.ErrNoAvatar) {
		t.Fatalf("GetForOrgMember outsider = %v, want ErrNoAvatar", err)
	}
}

// Purging a user must not leave their photo behind.
func TestAvatarIsRemovedWithTheUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := useravatar.NewStore(pool)
	ctx := context.Background()
	userID := makeUser(t, pool, "ann@example.test")

	if _, err := store.Save(ctx, userID, useravatar.Avatar{Bytes: []byte{1}, ContentType: "image/png"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM user_avatars WHERE user_id = $1", userID).Scan(&count); err != nil {
		t.Fatalf("count avatars: %v", err)
	}
	if count != 0 {
		t.Errorf("user_avatars rows = %d, want 0 after the user is deleted", count)
	}
}

func makeUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Test", "User").Scan(&id)
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return id
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

func addMembership(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, 'member')",
		orgID, userID); err != nil {
		t.Fatalf("add membership: %v", err)
	}
}
