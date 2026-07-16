//go:build integration

package presentation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/presentation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const ttl = time.Hour

func TestStoreCreateResolvesToTransactionID(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := presentation.NewStore(pool, ttl)
	ctx := context.Background()

	id, err := store.Create(ctx, "verifier-tx-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The client-facing id is not the verifier transaction id.
	if id == "verifier-tx-1" {
		t.Fatal("client id must not equal the verifier transaction id")
	}

	got, err := store.TransactionID(ctx, id)
	if err != nil {
		t.Fatalf("TransactionID: %v", err)
	}
	if got != "verifier-tx-1" {
		t.Errorf("transaction id = %q, want verifier-tx-1", got)
	}
}

func TestStoreUnknownIDIsNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := presentation.NewStore(pool, ttl)

	if _, err := store.TransactionID(context.Background(), "never-minted"); !errors.Is(err, presentation.ErrNotFound) {
		t.Errorf("TransactionID err = %v, want ErrNotFound", err)
	}
}

func TestStoreExpiredIDIsNotFoundAndPrunable(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := presentation.NewStore(pool, -time.Hour) // already expired on create
	ctx := context.Background()

	id, err := store.Create(ctx, "verifier-tx-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.TransactionID(ctx, id); !errors.Is(err, presentation.ErrNotFound) {
		t.Errorf("expired TransactionID err = %v, want ErrNotFound", err)
	}

	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteExpired removed %d rows, want 1", deleted)
	}
}
