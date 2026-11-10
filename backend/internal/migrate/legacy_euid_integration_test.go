//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// legacyEUIDVersion is 20261110105028_rewrite_organizations_legacy_euid.sql.
const legacyEUIDVersion int64 = 20261110105028

// TestUpRewritesLegacyEUIDs stores our own NL.KVK.<kvk> placeholder next to
// values it must not touch, on organizations and QERDS contacts: afterwards only
// the placeholders read NLNHR.<kvk>.
func TestUpRewritesLegacyEUIDs(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, legacyEUIDVersion-1); err != nil {
		t.Fatalf("migrate up to before the EUID rewrite: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows := []legacyEUIDRow{
		{"legacy", "NL.KVK.12345678", "NLNHR.12345678"},
		{"bris", "NLNHR.87654321", "NLNHR.87654321"},
		{"other", "DEHRB.12345", "DEHRB.12345"},
		{"not-a-kvk-number", "NL.KVK.acme", "NL.KVK.acme"},
	}
	seedLegacyEUIDRows(t, conn, rows)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	for _, r := range rows {
		var org, contact string
		if err := conn.QueryRow(ctx, `SELECT euid FROM organizations WHERE slug = $1`, r.key).Scan(&org); err != nil {
			t.Fatalf("read org %s: %v", r.key, err)
		}
		if err := conn.QueryRow(ctx, `SELECT euid FROM qerds_contacts WHERE address = $1`, r.key).Scan(&contact); err != nil {
			t.Fatalf("read contact %s: %v", r.key, err)
		}
		if org != r.want || contact != r.want {
			t.Errorf("%s: org euid = %q, contact euid = %q, want %q", r.key, org, contact, r.want)
		}
	}
	var none *string
	if err := conn.QueryRow(ctx, `SELECT euid FROM qerds_contacts WHERE address = 'none'`).Scan(&none); err != nil || none != nil {
		t.Errorf("contact without euid = %v, %v; want NULL", none, err)
	}
}

type legacyEUIDRow struct {
	key, before, want string
}

// seedLegacyEUIDRows stores each row's before value as an org and as a contact
// of the last org, plus one contact without an EUID.
func seedLegacyEUIDRows(t *testing.T, conn *pgx.Conn, rows []legacyEUIDRow) {
	t.Helper()
	ctx := context.Background()
	var orgID string
	for _, r := range rows {
		if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, $2, $1 || '@qerds.test') RETURNING id`, r.key, r.before).Scan(&orgID); err != nil {
			t.Fatalf("seed org %s: %v", r.key, err)
		}
	}
	for _, r := range rows {
		if _, err := conn.Exec(ctx, `INSERT INTO qerds_contacts (organization_id, name, address, euid)
			VALUES ($1, $2, $2, $3)`, orgID, r.key, r.before); err != nil {
			t.Fatalf("seed contact %s: %v", r.key, err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO qerds_contacts (organization_id, name, address)
		VALUES ($1, 'none', 'none')`, orgID); err != nil {
		t.Fatalf("seed contact without euid: %v", err)
	}
}
