//go:build integration

package session_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const sessionTTL = time.Hour

// createUser inserts a user and returns its id.
func createUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Test", "User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func TestStoreMintLookupRoundTrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := session.NewStore(pool, sessionTTL)
	ctx := context.Background()

	userID := createUser(t, pool, "alice@example.test")

	raw, err := store.Mint(ctx, userID, sha256.Sum256([]byte("key-1")))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	u, err := store.Lookup(ctx, raw)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if u.ID != userID {
		t.Errorf("looked-up user = %s, want %s", u.ID, userID)
	}
}

func TestStoreDeleteInvalidatesSession(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := session.NewStore(pool, sessionTTL)
	ctx := context.Background()

	userID := createUser(t, pool, "alice@example.test")
	raw, err := store.Mint(ctx, userID, sha256.Sum256([]byte("key-1")))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if err := store.Delete(ctx, raw); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Lookup(ctx, raw); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("Lookup after Delete err = %v, want ErrInvalidSession", err)
	}
}

func TestStoreMintRotatesOnSameIdempotencyKey(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := session.NewStore(pool, sessionTTL)
	ctx := context.Background()

	userID := createUser(t, pool, "alice@example.test")
	key := sha256.Sum256([]byte("replayed"))

	first, err := store.Mint(ctx, userID, key)
	if err != nil {
		t.Fatalf("first Mint: %v", err)
	}
	second, err := store.Mint(ctx, userID, key)
	if err != nil {
		t.Fatalf("second Mint: %v", err)
	}

	// The replayed mint rotates the single row: the old token is dead, the new
	// one works, and no second session was created.
	if _, err := store.Lookup(ctx, first); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("old token still valid; err = %v, want ErrInvalidSession", err)
	}
	if _, err := store.Lookup(ctx, second); err != nil {
		t.Errorf("new token Lookup: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1", userID).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("session rows = %d, want 1", count)
	}
}

func TestStoreExpiredSessionIsInvalidAndPrunable(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := session.NewStore(pool, -time.Hour) // already expired on mint
	ctx := context.Background()

	userID := createUser(t, pool, "alice@example.test")
	raw, err := store.Mint(ctx, userID, sha256.Sum256([]byte("key-1")))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if _, err := store.Lookup(ctx, raw); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expired Lookup err = %v, want ErrInvalidSession", err)
	}

	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteExpired removed %d rows, want 1", deleted)
	}
}

func TestStoreSessionsCascadeOnUserDelete(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := session.NewStore(pool, sessionTTL)
	ctx := context.Background()

	userID := createUser(t, pool, "alice@example.test")
	if _, err := store.Mint(ctx, userID, sha256.Sum256([]byte("key-1"))); err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1", userID).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("sessions after user delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}
