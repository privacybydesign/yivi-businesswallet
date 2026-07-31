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
		"INSERT INTO users (email, preferred_name, given_names, last_name) VALUES ($1, $2, $3, $4) RETURNING id",
		email, "Ally", "Alice Maria", "van Doorn",
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	got, err := store.FindByEmail(ctx, email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	preferred := "Ally"
	want := user.User{
		ID:            id,
		Email:         email,
		PreferredName: &preferred,
		GivenNames:    "Alice Maria",
		LastName:      "van Doorn",
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

func TestStoreAvatarRoundTrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)
	ctx := context.Background()

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"carol@example.test", "Carol", "Ceder",
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	if _, err := store.GetAvatar(ctx, id); !errors.Is(err, user.ErrNoAvatar) {
		t.Errorf("GetAvatar on a fresh user = %v, want user.ErrNoAvatar", err)
	}

	avatar := user.Avatar{Bytes: []byte{0xFF, 0xD8, 0xFF, 0xE0}, ContentType: user.AvatarContentType}
	stored, err := store.SetAvatar(ctx, id, avatar)
	if err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}
	if !stored.HasAvatar || stored.AvatarUpdatedAt == nil {
		t.Errorf("after SetAvatar: hasAvatar = %v, updatedAt = %v; want true and a timestamp", stored.HasAvatar, stored.AvatarUpdatedAt)
	}

	got, err := store.GetAvatar(ctx, id)
	if err != nil {
		t.Fatalf("GetAvatar: %v", err)
	}
	if !reflect.DeepEqual(got, avatar) {
		t.Errorf("GetAvatar = %+v, want %+v", got, avatar)
	}

	// The flag and the version also reach every other read of the user.
	fetched, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !fetched.HasAvatar {
		t.Error("GetByID does not report the stored avatar")
	}

	cleared, err := store.ClearAvatar(ctx, id)
	if err != nil {
		t.Fatalf("ClearAvatar: %v", err)
	}
	if cleared.HasAvatar || cleared.AvatarUpdatedAt != nil {
		t.Errorf("after ClearAvatar: hasAvatar = %v, updatedAt = %v; want false and nil", cleared.HasAvatar, cleared.AvatarUpdatedAt)
	}
	if _, err := store.GetAvatar(ctx, id); !errors.Is(err, user.ErrNoAvatar) {
		t.Errorf("GetAvatar after clearing = %v, want user.ErrNoAvatar", err)
	}
}

func TestStoreSetAvatarUnknownUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := user.NewStore(pool)

	_, err := store.SetAvatar(context.Background(), uuid.New(),
		user.Avatar{Bytes: []byte{1}, ContentType: user.AvatarContentType})
	if !errors.Is(err, user.ErrNotFound) {
		t.Errorf("err = %v, want user.ErrNotFound", err)
	}
}
