//go:build integration

package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func TestStoreFindByEmailReturnsUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)
	ctx := context.Background()

	const email = "alice@example.test"
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, preferred_name, given_names, name_prefix, last_name) VALUES ($1, $2, $3, $4, $5) RETURNING id",
		email, "Ally", "Alice Maria", "van", "Doorn",
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	got, err := store.FindByEmail(ctx, email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	prefix := "van"
	preferred := "Ally"
	want := user.User{
		ID:            id,
		Email:         email,
		PreferredName: &preferred,
		GivenNames:    "Alice Maria",
		NamePrefix:    &prefix,
		LastName:      "Doorn",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindByEmail = %+v, want %+v", got, want)
	}
}

func TestStoreFindByEmailNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)

	_, err := store.FindByEmail(context.Background(), "nobody@example.test")
	if !errors.Is(err, user.ErrNotFound) {
		t.Errorf("err = %v, want user.ErrNotFound", err)
	}
}

func TestStoreGetByIDReturnsCreatedUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)
	ctx := context.Background()

	const email = "bob@example.test"
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Bob", "Bouwer",
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	got, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	want := user.User{ID: id, Email: email, GivenNames: "Bob", LastName: "Bouwer"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetByID = %+v, want %+v", got, want)
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
