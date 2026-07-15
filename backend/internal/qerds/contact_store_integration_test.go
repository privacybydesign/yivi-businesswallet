//go:build integration

package qerds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestContactsRoundTrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	org, err := organization.NewStore(pool, audit.NopRecorder{}).Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	store := qerds.NewStore(pool, audit.NopRecorder{})

	created, err := store.CreateContact(ctx, org.ID, "Municipality", "muni@qerds.localhost")
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if created.Name != "Municipality" || created.Address != "muni@qerds.localhost" {
		t.Fatalf("unexpected contact: %+v", created)
	}

	// Same address within the org is rejected.
	if _, err := store.CreateContact(ctx, org.ID, "Dup", "muni@qerds.localhost"); !errors.Is(err, qerds.ErrContactAddressTaken) {
		t.Fatalf("duplicate address err = %v, want ErrContactAddressTaken", err)
	}

	contacts, err := store.ListContacts(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(contacts) != 1 {
		t.Fatalf("contacts = %d, want 1", len(contacts))
	}

	// Deleting an unknown id is a not-found, not a silent success.
	if err := store.DeleteContact(ctx, org.ID, uuid.New()); !errors.Is(err, qerds.ErrContactNotFound) {
		t.Fatalf("delete unknown err = %v, want ErrContactNotFound", err)
	}

	if err := store.DeleteContact(ctx, org.ID, created.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	remaining, err := store.ListContacts(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListContacts after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("contacts after delete = %d, want 0", len(remaining))
	}
}
