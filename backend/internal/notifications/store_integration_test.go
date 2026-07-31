//go:build integration

package notifications_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestGetSettingsUnconfigured(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")

	got, err := store.GetSettings(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured {
		t.Error("Configured = true, want false for an org that never saved subscriptions")
	}
	if len(got.Subscriptions) != 0 {
		t.Errorf("Subscriptions = %v, want none", got.Subscriptions)
	}
}

func TestSaveThenGetRoundtripsSubscriptions(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	in := notifications.SettingsInput{Subscriptions: map[string][]notifications.ChannelID{
		"membership.invited": {notifications.ChannelEmail, notifications.ChannelSlack},
		"attestation.issued": {notifications.ChannelTeams},
	}}
	saved, err := store.Save(ctx, orgID, in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved.Configured || saved.UpdatedAt == nil {
		t.Fatalf("Save returned %+v, want a configured document with a timestamp", saved)
	}

	got, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if len(got.ChannelsFor("membership.invited")) != 2 ||
		len(got.ChannelsFor("attestation.issued")) != 1 {
		t.Errorf("Subscriptions = %v, want the saved document", got.Subscriptions)
	}
}

func TestSaveReplacesTheWholeDocument(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	first := notifications.SettingsInput{Subscriptions: map[string][]notifications.ChannelID{
		"membership.invited": {notifications.ChannelEmail},
		"wallet.revoked":     {notifications.ChannelEmail},
	}}
	if _, err := store.Save(ctx, orgID, first); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second := notifications.SettingsInput{Subscriptions: map[string][]notifications.ChannelID{
		"membership.invited": {notifications.ChannelSlack},
	}}
	got, err := store.Save(ctx, orgID, second)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(got.Subscriptions) != 1 || len(got.ChannelsFor("wallet.revoked")) != 0 {
		t.Errorf("Subscriptions = %v, want only the second document", got.Subscriptions)
	}
}

func TestSaveAuditsTheChange(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	in := notifications.SettingsInput{Subscriptions: map[string][]notifications.ChannelID{
		"membership.invited": {notifications.ChannelEmail},
	}}
	if _, err := store.Save(ctx, orgID, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var action, targetType string
	err := pool.QueryRow(ctx,
		`SELECT action, target_type FROM audit_events WHERE organization_id = $1`, orgID).
		Scan(&action, &targetType)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if action != audit.NotificationSettingsUpdated || targetType != audit.TargetNotificationSettings {
		t.Errorf("audited %s/%s, want %s/%s", action, targetType,
			audit.NotificationSettingsUpdated, audit.TargetNotificationSettings)
	}
}

// TestRecorderEnqueuesOnCommit and TestRecorderEnqueuesNothingOnRollback are the
// heart of the layer: an event is queued for notification if and only if the
// action that caused it committed.
func TestRecorderEnqueuesOnCommit(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	recorder := notifications.NewRecorder(audit.NewDBRecorder(), store)
	orgID := makeOrg(t, pool, "acme")
	userID := makeUser(t, pool, "actor@example.org")
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{UserID: userID})

	err := database.InTx(ctx, pool, func(q database.Querier) error {
		return recorder.Record(ctx, q, audit.MembershipInvited,
			audit.Target{Type: audit.TargetMembership, ID: "member-1", OrgID: &orgID},
			audit.Created(map[string]any{"email": "invitee@example.org"}))
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	claimed, err := store.Claim(context.Background(), 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d events, want 1", len(claimed))
	}
	got := claimed[0]
	if got.OrgID != orgID || got.Action != audit.MembershipInvited || got.TargetID != "member-1" {
		t.Errorf("claimed %+v, want the recorded event", got)
	}
	if got.ActorUserID == nil || *got.ActorUserID != userID {
		t.Errorf("ActorUserID = %v, want %s", got.ActorUserID, userID)
	}
	if got.Metadata == nil {
		t.Error("the claimed event lost its metadata")
	}

	// Claiming removes the row, so a second pass finds nothing.
	again, err := store.Claim(context.Background(), 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("claimed %d events on the second pass, want none", len(again))
	}
}

func TestRecorderEnqueuesNothingOnRollback(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	recorder := notifications.NewRecorder(audit.NewDBRecorder(), store)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	boom := errors.New("the action failed after the audit write")
	err := database.InTx(ctx, pool, func(q database.Querier) error {
		if err := recorder.Record(ctx, q, audit.MembershipInvited,
			audit.Target{Type: audit.TargetMembership, ID: "member-1", OrgID: &orgID}, nil); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want the rollback error", err)
	}

	claimed, err := store.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d events, want none for a rolled back action", len(claimed))
	}
}

func TestRecorderSkipsEventsOutsideTheCatalog(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	recorder := notifications.NewRecorder(audit.NewDBRecorder(), store)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	err := database.InTx(ctx, pool, func(q database.Querier) error {
		return recorder.Record(ctx, q, audit.ThemeSettingsUpdated,
			audit.Target{Type: audit.TargetThemeSettings, ID: orgID.String(), OrgID: &orgID}, nil)
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	claimed, err := store.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d events, want none for an event outside the catalog", len(claimed))
	}
}

func TestClaimTakesTheOldestFirstUpToTheLimit(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := notifications.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	for _, target := range []string{"member-1", "member-2", "member-3"} {
		e := notifications.Event{
			OrgID:      orgID,
			Action:     audit.MembershipInvited,
			TargetType: audit.TargetMembership,
			TargetID:   target,
		}
		if err := store.Enqueue(ctx, pool, e); err != nil {
			t.Fatalf("Enqueue %s: %v", target, err)
		}
	}

	claimed, err := store.Claim(ctx, 2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d events, want the batch limit of 2", len(claimed))
	}

	rest, err := store.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(rest) != 1 {
		t.Errorf("claimed %d events on the second pass, want the remaining 1", len(rest))
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

func makeUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Test", "Actor").Scan(&id)
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return id
}
