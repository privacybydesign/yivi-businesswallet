//go:build integration

package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func TestStoreFindOrCreateByEmailIsIdempotent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)
	ctx := context.Background()

	const email = "alice@example.test"

	first, err := store.FindOrCreateByEmail(ctx, email)
	if err != nil {
		t.Fatalf("first FindOrCreateByEmail: %v", err)
	}
	second, err := store.FindOrCreateByEmail(ctx, email)
	if err != nil {
		t.Fatalf("second FindOrCreateByEmail: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ID changed across calls: %s != %s", first.ID, second.ID)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = $1", email).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("user rows = %d, want 1", count)
	}
}

func TestStoreGetByIDReturnsCreatedUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)
	ctx := context.Background()

	created, err := store.FindOrCreateByEmail(ctx, "bob@example.test")
	if err != nil {
		t.Fatalf("FindOrCreateByEmail: %v", err)
	}

	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != created {
		t.Errorf("GetByID = %+v, want %+v", got, created)
	}
}

func TestStoreGetByIDNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)

	_, err := store.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, user.ErrNotFound) {
		t.Errorf("err = %v, want user.ErrNotFound", err)
	}
}
