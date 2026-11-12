//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// beforeKeyWalletUnitAttestation is the last migration before the
// attestation_keys.wallet_unit_attestation column.
const beforeKeyWalletUnitAttestation int64 = 20261111101107

func hasKeyWalletUnitAttestation(t *testing.T, ctx context.Context, conn *pgx.Conn) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'attestation_keys' AND column_name = 'wallet_unit_attestation')`).Scan(&exists); err != nil {
		t.Fatalf("read the columns: %v", err)
	}
	return exists
}

func TestKeyWalletUnitAttestationUpAndDown(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if !hasKeyWalletUnitAttestation(t, ctx, conn) {
		t.Fatal("up did not add attestation_keys.wallet_unit_attestation")
	}
	downTo(t, ctx, dsn, beforeKeyWalletUnitAttestation)
	if hasKeyWalletUnitAttestation(t, ctx, conn) {
		t.Fatal("down left attestation_keys.wallet_unit_attestation")
	}
	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}
