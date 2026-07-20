//go:build integration

package wsca_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

const testKEK = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 32 bytes hex

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

func TestStoreActivateSecretRotate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	org := seedOrg(t, ctx, pool, "acme")

	cipher, err := crypto.NewCipher(testKEK)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	s := wsca.NewStore(pool, cipher)

	// Not activated yet.
	if _, err := s.Get(ctx, org); !errors.Is(err, wsca.ErrNotActivated) {
		t.Fatalf("Get before activate = %v, want ErrNotActivated", err)
	}
	if _, err := s.Secret(ctx, org); !errors.Is(err, wsca.ErrNotActivated) {
		t.Fatalf("Secret before activate = %v, want ErrNotActivated", err)
	}

	// Activate: seal the secret, record account + cert.
	const secret = "12345678901234567890"
	acc, err := s.Activate(ctx, org, secret, "wsca-account-hash", "cert-1")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if acc.AccountID != "wsca-account-hash" || acc.CertificateID != "cert-1" {
		t.Errorf("account = %+v", acc)
	}
	if acc.RotatedAt != nil {
		t.Error("RotatedAt should be nil right after activation")
	}

	// Secret round-trips through seal/open.
	got, err := s.Secret(ctx, org)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if got != secret {
		t.Errorf("Secret = %q, want %q", got, secret)
	}

	// Ciphertext at rest must not equal the plaintext.
	var ct []byte
	if err := pool.QueryRow(ctx, `SELECT secret_ciphertext FROM org_wsca_accounts WHERE organization_id=$1`, org).Scan(&ct); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if strings.Contains(string(ct), secret) {
		t.Error("secret stored in plaintext")
	}

	// Rotate: new secret + cert, account id unchanged, rotated_at set.
	const newSecret = "09876543210987654321"
	rot, err := s.Rotate(ctx, org, newSecret, "cert-2")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rot.AccountID != "wsca-account-hash" {
		t.Errorf("account id changed on rotate: %q", rot.AccountID)
	}
	if rot.CertificateID != "cert-2" || rot.RotatedAt == nil {
		t.Errorf("rotate did not update cert/rotated_at: %+v", rot)
	}
	if got, _ := s.Secret(ctx, org); got != newSecret {
		t.Errorf("Secret after rotate = %q, want %q", got, newSecret)
	}

	// Rotating a non-existent org is ErrNotActivated.
	if _, err := s.Rotate(ctx, uuid.New(), "x", "c"); !errors.Is(err, wsca.ErrNotActivated) {
		t.Errorf("Rotate unknown org = %v, want ErrNotActivated", err)
	}
}
