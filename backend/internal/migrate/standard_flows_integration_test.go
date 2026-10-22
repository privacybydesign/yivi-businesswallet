//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// standardFlowsVersion is 20261021122000_add_identity_proofing_flow_settings_standard.sql.
const standardFlowsVersion int64 = 20261021122000

// An org's oldest flow of each data request kind becomes its standard flow:
// by the engine's first version, else (no engine flow) by its settings. The
// others stay data request flows. Every data request flow's hosted page is on
// again, one standard flow per kind is all there can be, and an identity flow
// is never one. Down drops the mark.
func TestUpMarksStandardFlows(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, standardFlowsVersion-1); err != nil {
		t.Fatalf("migrate up to before the standard flows: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Standard BV', 'standard', '11223344', 'NLNHR.11223344', 'standard@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	// a-old's engine flow is older than a-new's, though its settings were
	// written later; e-stub has no engine flow; id is an identity check.
	for _, v := range []struct{ flow, created string }{
		{"a-old", "2026-01-01"}, {"a-new", "2026-02-01"}, {"id", "2025-12-01"},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_flow_versions (organization_id, flow_id, version, definition, active, created_at)
			VALUES ($1, $2, 1, '{}', true, $3)`, orgID, v.flow, v.created); err != nil {
			t.Fatalf("insert engine flow %s: %v", v.flow, err)
		}
	}
	for _, s := range []struct{ flow, kind, created string }{
		{"a-new", "data_access", "2026-03-01"},
		{"a-old", "data_access", "2026-04-01"},
		{"e-stub", "data_erasure", "2026-03-01"},
		{"id", "identity", "2026-03-01"},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, kind, created_at)
			VALUES ($1, $2, $3, $4)`, orgID, s.flow, s.kind, s.created); err != nil {
			t.Fatalf("insert settings %s: %v", s.flow, err)
		}
	}
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_flow_settings
		SET hosted_enabled = false, hosted_locales = '{nl}', hosted_completion = 'done' WHERE flow_id IN ('a-new', 'id')`); err != nil {
		t.Fatalf("switch hosted pages off: %v", err)
	}

	if err := migrate.UpTo(ctx, dsn, standardFlowsVersion); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	for flow, want := range map[string]bool{"a-old": true, "a-new": false, "e-stub": true, "id": false} {
		var standard bool
		if err := conn.QueryRow(ctx, `SELECT standard FROM identity_proofing_flow_settings WHERE flow_id = $1`, flow).Scan(&standard); err != nil {
			t.Fatalf("read %s: %v", flow, err)
		}
		if standard != want {
			t.Errorf("%s standard = %v, want %v", flow, standard, want)
		}
	}
	assertCount(t, ctx, conn, "data request flows with the default hosted page", 3,
		`SELECT count(*) FROM identity_proofing_flow_settings
		WHERE kind <> 'identity' AND hosted_enabled AND hosted_locales = '{}' AND hosted_completion = 'redirect'`)
	assertCount(t, ctx, conn, "the identity flow's hosted page as it was", 1,
		`SELECT count(*) FROM identity_proofing_flow_settings WHERE flow_id = 'id' AND NOT hosted_enabled AND hosted_completion = 'done'`)

	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_flow_settings SET standard = true WHERE flow_id = 'a-new'`); sqlState(err) != uniqueViolation {
		t.Errorf("a second standard See my data flow = %v, want a unique violation", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_flow_settings SET standard = true WHERE flow_id = 'id'`); sqlState(err) != checkViolation {
		t.Errorf("a standard identity flow = %v, want a check violation", err)
	}

	if err := migrate.Down(ctx, dsn); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertCount(t, ctx, conn, "the standard column after down", 0,
		`SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'identity_proofing_flow_settings' AND column_name = 'standard'`)
}
