//go:build integration

package wscawallet

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

// TestActivatorActivateRotate exercises the full Activator flow against a real
// sealed-secret store with a fake wallet client (no live WSCA): activate seals
// the secret and records the first cert as the stable account; rotate keeps the
// account and swaps the cert; the sealed secret updates.
func TestActivatorActivateRotate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	org := seedOrg(t, ctx, pool, "acme")

	cipher, err := crypto.NewCipher(testKEK)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	store := wsca.NewStore(pool, cipher)
	fake := &fakeWallet{}
	a := NewActivator(true, func(uuid.UUID) (WalletClient, error) { return fake, nil }, store)

	// Not activated yet.
	if _, err := a.Status(ctx, org); !errors.Is(err, wsca.ErrNotActivated) {
		t.Fatalf("Status before activate = %v, want ErrNotActivated", err)
	}

	// Activate: fake wallet activates (cert-1) and the secret is sealed.
	acc, err := a.Activate(ctx, org, "123456")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if acc.AccountID != "cert-1" || acc.CertificateID != "cert-1" || acc.RotatedAt != nil {
		t.Fatalf("account after activate = %+v", acc)
	}
	if fake.activateCalls != 1 {
		t.Errorf("wallet Activate calls = %d, want 1", fake.activateCalls)
	}
	if got, err := store.Secret(ctx, org); err != nil || got != "123456" {
		t.Fatalf("sealed secret = %q, %v; want 123456", got, err)
	}

	// Activate again is a no-op conflict (wallet already activated).
	if _, err := a.Activate(ctx, org, "123456"); !errors.Is(err, ErrAlreadyActivated) {
		t.Fatalf("second Activate = %v, want ErrAlreadyActivated", err)
	}

	// Rotate: fake ChangePIN issues cert-2; account id stays, secret updates.
	rotated, err := a.Rotate(ctx, org, "123456", "654321")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.AccountID != "cert-1" || rotated.CertificateID != "cert-2" || rotated.RotatedAt == nil {
		t.Fatalf("account after rotate = %+v", rotated)
	}
	if fake.changeCalls != 1 {
		t.Errorf("wallet ChangePIN calls = %d, want 1", fake.changeCalls)
	}
	if got, err := store.Secret(ctx, org); err != nil || got != "654321" {
		t.Fatalf("sealed secret after rotate = %q, %v; want 654321", got, err)
	}
}

// TestActivatorRotateNotActivated: rotating before activation is rejected without
// touching the wallet.
func TestActivatorRotateNotActivated(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	org := seedOrg(t, ctx, pool, "acme")

	cipher, err := crypto.NewCipher(testKEK)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	fake := &fakeWallet{}
	a := NewActivator(true, func(uuid.UUID) (WalletClient, error) { return fake, nil }, wsca.NewStore(pool, cipher))

	if _, err := a.Rotate(ctx, org, "123456", "654321"); !errors.Is(err, wsca.ErrNotActivated) {
		t.Fatalf("Rotate before activate = %v, want ErrNotActivated", err)
	}
	if fake.changeCalls != 0 {
		t.Errorf("wallet ChangePIN should not be called; got %d", fake.changeCalls)
	}
}
