//go:build integration

package email

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// testKey is a throwaway AES-256 key; the deployment's real one comes from
// EMAIL_ENCRYPTION_KEY.
const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newSettingsStore(t *testing.T, pool *pgxpool.Pool) *Store {
	t.Helper()
	cipher, err := crypto.NewCipher(testKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return NewStore(pool, audit.NopRecorder{}, cipher)
}

func newOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
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

func xoauth2Input() SettingsInput {
	secret := "s3cret"
	return SettingsInput{
		Host:          "smtp.office365.com",
		Port:          587,
		AuthMechanism: mailer.AuthXOAuth2,
		TenantID:      "tenant-1",
		ClientID:      "client-1",
		ClientSecret:  &secret,
		FromAddress:   "no-reply@acme.example",
		Enabled:       true,
	}
}

// The app registration round-trips, and the client secret comes back only as a
// flag: the settings screen must never receive it.
func TestUpsertRoundtripsAnXOAuth2Configuration(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newSettingsStore(t, pool)
	orgID := newOrg(t, pool, "acme")

	got, err := store.Upsert(context.Background(), orgID, xoauth2Input())
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.AuthMechanism != mailer.AuthXOAuth2 {
		t.Errorf("AuthMechanism = %q, want %q", got.AuthMechanism, mailer.AuthXOAuth2)
	}
	if got.TenantID != "tenant-1" || got.ClientID != "client-1" {
		t.Errorf("app registration = %q/%q, want tenant-1/client-1", got.TenantID, got.ClientID)
	}
	if !got.HasClientSecret {
		t.Error("HasClientSecret = false, want true")
	}
}

// The secret is encrypted at rest under the deployment e-mail key, like the SMTP
// password beside it, and comes back out for a send.
func TestClientSecretIsEncryptedAtRestAndResolvesForASend(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newSettingsStore(t, pool)
	orgID := newOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Upsert(ctx, orgID, xoauth2Input()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var stored []byte
	if err := pool.QueryRow(ctx,
		`SELECT client_secret_ciphertext FROM org_email_settings WHERE organization_id = $1`,
		orgID).Scan(&stored); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if strings.Contains(string(stored), "s3cret") {
		t.Error("the client secret is stored in the clear")
	}

	cfg, ok, err := store.configFor(ctx, orgID)
	if err != nil || !ok {
		t.Fatalf("configFor: %v (ok=%t)", err, ok)
	}
	if cfg.OAuth.ClientSecret != "s3cret" {
		t.Errorf("ClientSecret = %q, want the stored secret", cfg.OAuth.ClientSecret)
	}
	if cfg.Mailer.AuthMechanism != mailer.AuthXOAuth2 {
		t.Errorf("AuthMechanism = %q, want %q", cfg.Mailer.AuthMechanism, mailer.AuthXOAuth2)
	}
}

// A save that does not repeat the secret keeps the stored one; an explicit empty
// string clears it. The two are different requests and must stay so, because the
// settings screen never gets the secret back to resend.
func TestUpsertKeepsAndClearsTheClientSecret(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newSettingsStore(t, pool)
	orgID := newOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Upsert(ctx, orgID, xoauth2Input()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	kept := xoauth2Input()
	kept.ClientSecret = nil
	kept.ClientID = "client-2"
	got, err := store.Upsert(ctx, orgID, kept)
	if err != nil {
		t.Fatalf("Upsert (keep): %v", err)
	}
	if !got.HasClientSecret {
		t.Error("omitting the secret cleared it")
	}
	if got.ClientID != "client-2" {
		t.Errorf("ClientID = %q, want the updated client-2", got.ClientID)
	}

	cleared := xoauth2Input()
	empty := ""
	cleared.ClientSecret = &empty
	got, err = store.Upsert(ctx, orgID, cleared)
	if err != nil {
		t.Fatalf("Upsert (clear): %v", err)
	}
	if got.HasClientSecret {
		t.Error("an empty secret did not clear the stored one")
	}
}

// The password path is untouched by the OAuth columns: an org that never
// configured XOAUTH2 reads back as a password org with no app registration.
func TestUpsertLeavesAPasswordOrgOnTheDefaultMechanism(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newSettingsStore(t, pool)
	orgID := newOrg(t, pool, "acme")
	password := "pw"

	got, err := store.Upsert(context.Background(), orgID, SettingsInput{
		Host: "mail.example.org", Port: 587, Username: "acme", Password: &password,
		FromAddress: "no-reply@acme.example", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.AuthMechanism != mailer.AuthPlain {
		t.Errorf("AuthMechanism = %q, want %q", got.AuthMechanism, mailer.AuthPlain)
	}
	if got.HasClientSecret || got.TenantID != "" || got.ClientID != "" {
		t.Error("a password org came back carrying an app registration")
	}
	if !got.HasPassword {
		t.Error("HasPassword = false, want true")
	}
}

// An org that has never saved settings still opens the screen on a selected
// mechanism rather than a blank one.
func TestGetSettingsDefaultsTheMechanismForAnUnconfiguredOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newSettingsStore(t, pool)
	orgID := newOrg(t, pool, "acme")

	got, err := store.GetSettings(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured {
		t.Error("Configured = true for an org with no settings")
	}
	if got.AuthMechanism != mailer.AuthPlain {
		t.Errorf("AuthMechanism = %q, want %q", got.AuthMechanism, mailer.AuthPlain)
	}
}
