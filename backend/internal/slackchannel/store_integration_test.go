//go:build integration

// The tests live in the package rather than beside it because webhookFor — the
// read the channel delivers through — is deliberately unexported: the decrypted
// URL never leaves this package.
package slackchannel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// testEncryptionKey is a throwaway AES-256 key (hex 32 bytes).
const testEncryptionKey = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

const testWebhook = "https://hooks.slack.com/services/T0TEST/B0TEST/tokentokentoken"

func newTestStore(t *testing.T, pool *pgxpool.Pool, recorder audit.Recorder) *Store {
	t.Helper()
	cipher, err := crypto.NewCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("build cipher: %v", err)
	}
	return NewStore(pool, recorder, cipher)
}

func TestGetSettingsWithoutASavedWebhook(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")

	got, err := store.GetSettings(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured || got.HasWebhook || got.Enabled {
		t.Errorf("GetSettings = %+v, want the zero settings for an org that never saved any", got)
	}
}

// The row must never hold the plaintext URL: whoever reads the table would
// otherwise be able to post as the workspace's integration.
func TestUpsertStoresTheWebhookEncrypted(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	webhook := testWebhook
	got, err := store.Upsert(ctx, orgID, SettingsInput{WebhookURL: &webhook, Enabled: true})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !got.Configured || !got.HasWebhook || !got.Enabled || got.UpdatedAt == nil {
		t.Fatalf("Upsert returned %+v, want a configured, enabled row with a webhook", got)
	}

	var stored []byte
	err = pool.QueryRow(ctx,
		`SELECT webhook_url_ciphertext FROM org_slack_settings WHERE organization_id = $1`, orgID).
		Scan(&stored)
	if err != nil {
		t.Fatalf("read the stored ciphertext: %v", err)
	}
	if strings.Contains(string(stored), "hooks.slack.com") {
		t.Error("the stored value carries the plaintext webhook url")
	}

	resolved, err := store.webhookFor(ctx, orgID)
	if err != nil {
		t.Fatalf("webhookFor: %v", err)
	}
	if resolved != webhook {
		t.Errorf("webhookFor = %q, want the saved url", resolved)
	}
}

// A nil URL is "keep the stored one", which is what lets an admin switch delivery
// off and on again without pasting the webhook a second time.
func TestUpsertWithoutAURLKeepsTheStoredOne(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	webhook := testWebhook
	if _, err := store.Upsert(ctx, orgID, SettingsInput{WebhookURL: &webhook, Enabled: true}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Upsert(ctx, orgID, SettingsInput{Enabled: false})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !got.HasWebhook || got.Enabled {
		t.Errorf("Upsert returned %+v, want the webhook kept and delivery off", got)
	}

	// Off means off: the channel resolves nothing for this org.
	if _, err := store.webhookFor(ctx, orgID); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("webhookFor = %v, want ErrNotConfigured while delivery is off", err)
	}
}

func TestUpsertWithAnEmptyURLClearsIt(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	webhook := testWebhook
	if _, err := store.Upsert(ctx, orgID, SettingsInput{WebhookURL: &webhook, Enabled: true}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	cleared := ""
	got, err := store.Upsert(ctx, orgID, SettingsInput{WebhookURL: &cleared, Enabled: true})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.HasWebhook || got.Enabled {
		t.Errorf("Upsert returned %+v, want the webhook cleared and delivery off with it", got)
	}
	if _, err := store.webhookFor(ctx, orgID); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("webhookFor = %v, want ErrNotConfigured after clearing the webhook", err)
	}
}

// Delivery on with no webhook is a state the settings screen cannot render back:
// GET would answer enabled with hasWebhook false, which it shows as switched off.
// Only the API can ask for it, and the row is what both sides read, so it is
// clamped here (see nextState).
func TestUpsertCannotEnableWithoutAWebhook(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")

	got, err := store.Upsert(context.Background(), orgID, SettingsInput{Enabled: true})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Enabled || got.HasWebhook {
		t.Errorf("Upsert returned %+v, want delivery off while no webhook is stored", got)
	}
}

// Without a deployment key the secret cannot be held at all, so the save is
// refused rather than stored in the clear.
func TestUpsertWithoutAnEncryptionKeyIsRefused(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")

	webhook := testWebhook
	_, err := store.Upsert(context.Background(), orgID, SettingsInput{WebhookURL: &webhook, Enabled: true})
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("Upsert = %v, want ErrNoEncryptionKey", err)
	}

	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM org_slack_settings WHERE organization_id = $1`, orgID).Scan(&rows); err != nil {
		t.Fatalf("count settings rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("stored %d rows, want none for a refused save", rows)
	}
}

// The change is audited like every other settings change — and the audit event,
// which every org admin can read and export, carries whether a webhook is set
// rather than the URL itself.
func TestUpsertAuditsTheChangeWithoutTheURL(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newTestStore(t, pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	webhook := testWebhook
	if _, err := store.Upsert(ctx, orgID, SettingsInput{WebhookURL: &webhook, Enabled: true}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var action, targetType, metadata string
	err := pool.QueryRow(ctx,
		`SELECT action, target_type, metadata::text FROM audit_events WHERE organization_id = $1`, orgID).
		Scan(&action, &targetType, &metadata)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if action != audit.SlackSettingsUpdated || targetType != audit.TargetSlackSettings {
		t.Errorf("audited %s/%s, want %s/%s", action, targetType,
			audit.SlackSettingsUpdated, audit.TargetSlackSettings)
	}
	if strings.Contains(metadata, "hooks.slack.com") {
		t.Errorf("metadata = %s, want no webhook url in it", metadata)
	}
	if !strings.Contains(metadata, "hasWebhook") {
		t.Errorf("metadata = %s, want it to state whether a webhook is set", metadata)
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
