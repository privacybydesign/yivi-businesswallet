//go:build integration

package postguard_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/postguard"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestNotificationDeliveryDefaultsToPostGuard(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := postguard.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")

	got, err := store.NotificationDelivery(context.Background(), orgID)
	if err != nil {
		t.Fatalf("NotificationDelivery: %v", err)
	}
	if got != postguard.NotifyPostGuard {
		t.Errorf("default delivery = %q, want %q", got, postguard.NotifyPostGuard)
	}
}

func TestSetNotificationDeliveryRoundtrips(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := postguard.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if err := store.SetNotificationDelivery(ctx, orgID, postguard.NotifySMTP); err != nil {
		t.Fatalf("SetNotificationDelivery smtp: %v", err)
	}
	got, err := store.NotificationDelivery(ctx, orgID)
	if err != nil {
		t.Fatalf("NotificationDelivery: %v", err)
	}
	if got != postguard.NotifySMTP {
		t.Errorf("delivery = %q, want %q", got, postguard.NotifySMTP)
	}

	// Upsert back to PostGuard to confirm the conflict path updates in place.
	if err := store.SetNotificationDelivery(ctx, orgID, postguard.NotifyPostGuard); err != nil {
		t.Fatalf("SetNotificationDelivery postguard: %v", err)
	}
	got, err = store.NotificationDelivery(ctx, orgID)
	if err != nil {
		t.Fatalf("NotificationDelivery: %v", err)
	}
	if got != postguard.NotifyPostGuard {
		t.Errorf("delivery after update = %q, want %q", got, postguard.NotifyPostGuard)
	}
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
