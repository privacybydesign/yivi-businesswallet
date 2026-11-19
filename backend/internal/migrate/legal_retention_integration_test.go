//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// Versions around the legal retention (#335).
const (
	// beforeLegalRetention is the last migration before it.
	beforeLegalRetention int64 = 20261021101100
	// legalRetentionVersion adds it.
	legalRetentionVersion int64 = 20261021110000
	// erasureKeepVersion drops the "keep" erasure handling.
	erasureKeepVersion int64 = 20261021110100
	// weekRetentionVersion makes a week the default retention.
	weekRetentionVersion int64 = 20261021110200
)

// downTo steps the schema down until it is at version or below.
func downTo(t *testing.T, ctx context.Context, dsn string, version int64) {
	t.Helper()
	for {
		current, err := migrate.Version(ctx, dsn)
		if err != nil {
			t.Fatalf("read the version: %v", err)
		}
		if current <= version {
			return
		}
		if err := migrate.Down(ctx, dsn); err != nil {
			t.Fatalf("migrate down from %d: %v", current, err)
		}
	}
}

// insertOrg inserts an org whose KVK number is kvk, eight digits.
func insertOrg(t *testing.T, ctx context.Context, conn *pgx.Conn, slug, kvk string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $1, $2, 'NLNHR.' || $2, $1 || '@qerds.test') RETURNING id`, slug, kvk).Scan(&id); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	return id
}

// legalOrg inserts an org with a data retention of retentionDays and, when
// months is not 0, a legal retention.
func legalOrg(t *testing.T, ctx context.Context, conn *pgx.Conn, slug, kvk string, retentionDays, months int) string {
	t.Helper()
	orgID := insertOrg(t, ctx, conn, slug, kvk)
	if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_settings
			(organization_id, data_retention_days, legal_retention_months, legal_retention_reason_en, legal_retention_reason_nl)
		VALUES ($1, $2, NULLIF($3, 0), CASE WHEN $3 > 0 THEN 'Kept by law.' END, CASE WHEN $3 > 0 THEN 'Bewaard.' END)`,
		orgID, retentionDays, months); err != nil {
		t.Fatalf("insert settings: %v", err)
	}
	return orgID
}

// approvedCheck inserts an external identity check approved 40 days ago,
// purged at purgeAt (a Postgres expression over completed_at), with an
// optional retention override in seconds (0 for none).
func approvedCheck(t *testing.T, ctx context.Context, conn *pgx.Conn, orgID, purgeAt string, overrideSeconds int) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_kind, subject_name, subject_email, flow_id, flow_name, link_expires_at,
			 status, completed_at, retention_override_seconds)
		VALUES ($1, 'external', 'Anna', 'anna@example.org', 'f', 'Flow', now() - interval '40 days',
			'approved', now() - interval '40 days', NULLIF($2, 0)) RETURNING id`, orgID, overrideSeconds).Scan(&id); err != nil {
		t.Fatalf("insert check: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_requests SET purge_at = `+purgeAt+` WHERE id = $1`, id); err != nil {
		t.Fatalf("set purge_at: %v", err)
	}
	return id
}

// Without the legal retention a check is purged its own retention after it
// ended again: the down migration does not leave the years the law added.
func TestLegalRetentionDownRestoresPurgeTime(t *testing.T) {
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

	held := legalOrg(t, ctx, conn, "held", "10000001", 30, 60)
	plain := approvedCheck(t, ctx, conn, held, `completed_at + interval '60 months'`, 0)
	week := approvedCheck(t, ctx, conn, held, `completed_at + interval '60 months'`, 7*24*60*60)
	// Another org's check, without a legal retention, keeps what it had.
	free := legalOrg(t, ctx, conn, "free", "10000002", 90, 0)
	untouched := approvedCheck(t, ctx, conn, free, `completed_at + interval '3 days'`, 0)

	downTo(t, ctx, dsn, beforeLegalRetention)

	for _, tc := range []struct {
		name, id, want string
	}{
		{"the org's retention", plain, "30 days"},
		{"the flow's override", week, "7 days"},
		{"no legal retention", untouched, "3 days"},
	} {
		var ok bool
		if err := conn.QueryRow(ctx, `SELECT purge_at = completed_at + $2::interval
			FROM identity_proofing_requests WHERE id = $1`, tc.id, tc.want).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: purge_at is completed_at + %s = %v, %v", tc.name, tc.want, ok, err)
		}
	}
	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

// "keep" goes: an org that had it is decided by review, and it cannot be set
// again; the down migration allows it again.
func TestErasureKeepBecomesReview(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, legalRetentionVersion); err != nil {
		t.Fatalf("migrate up to the legal retention: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	orgID := legalOrg(t, ctx, conn, "keeper", "10000003", 30, 0)
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_settings SET erasure_handling = 'keep' WHERE organization_id = $1`, orgID); err != nil {
		t.Fatalf("set keep: %v", err)
	}

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	var handling string
	if err := conn.QueryRow(ctx, `SELECT erasure_handling FROM identity_proofing_settings WHERE organization_id = $1`, orgID).Scan(&handling); err != nil || handling != "review" {
		t.Errorf("erasure handling = %q, %v; want review", handling, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_settings SET erasure_handling = 'keep' WHERE organization_id = $1`, orgID); sqlState(err) != checkViolation {
		t.Errorf("setting keep = %v; want a check violation", err)
	}

	downTo(t, ctx, dsn, erasureKeepVersion-1)
	if _, err := conn.Exec(ctx, `UPDATE identity_proofing_settings SET erasure_handling = 'keep' WHERE organization_id = $1`, orgID); err != nil {
		t.Errorf("setting keep after the down migration: %v", err)
	}
}

// A new org's settings purge a week after a check ends; an org that sent an
// external request without saving settings keeps the 30 days it had. The test
// stops there: 20261021114200 later fixes every org at a week, once the 30
// days are copied into its flows and requests (TestUpCopiesOrgDefaultsIntoFlows).
func TestWeekRetentionForNewOrgs(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, erasureKeepVersion); err != nil {
		t.Fatalf("migrate up to the erasure handling: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	sender, idle := insertOrg(t, ctx, conn, "sender", "10000004"), insertOrg(t, ctx, conn, "idle", "10000005")
	if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_kind, subject_name, subject_email, flow_id, flow_name, link_expires_at)
		VALUES ($1, 'external', 'Anna', 'anna@example.org', 'f', 'Flow', now())`, sender); err != nil {
		t.Fatalf("insert request: %v", err)
	}

	if err := migrate.UpTo(ctx, dsn, weekRetentionVersion); err != nil {
		t.Fatalf("migrate up to the week retention: %v", err)
	}
	var days int
	if err := conn.QueryRow(ctx, `SELECT data_retention_days FROM identity_proofing_settings WHERE organization_id = $1`, sender).Scan(&days); err != nil || days != 30 {
		t.Errorf("retention of the org that sent a request = %d, %v; want the 30 days it had", days, err)
	}
	assertCount(t, ctx, conn, "settings of the org that sent nothing", 0,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id = $1`, idle)
	if err := conn.QueryRow(ctx, `INSERT INTO identity_proofing_settings (organization_id) VALUES ($1)
		RETURNING data_retention_days`, idle).Scan(&days); err != nil || days != 7 {
		t.Errorf("a new row's retention = %d, %v; want a week", days, err)
	}
}
