//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// memberPurgeBackfillVersion is
// 20261012120200_backfill_identity_proofing_member_purge_at.sql.
const memberPurgeBackfillVersion int64 = 20261012120200

// memberRetention is proofing.proofedNameRetention, which the backfill states
// in SQL.
const memberRetention = 30 * 24 * time.Hour

// The backfill gives an unpurged member request the purge time the store's
// rule does, and leaves a purged one and one on a flow retention as they were.
func TestBackfillMemberPurgeAt(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, memberPurgeBackfillVersion-1); err != nil {
		t.Fatalf("migrate up to before %d: %v", memberPurgeBackfillVersion, err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID, userID string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Backfill BV', 'backfill', '12345678', 'NLNHR.12345678', 'backfill@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO users (email, given_names, last_name) VALUES ('sam@example.org', 'Sam', 'Member') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	completed := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	flowPurge := completed.Add(time.Hour)
	insert := func(status string, completedAt, purgedAt, purgeAt *time.Time, overrideSeconds *int) string {
		t.Helper()
		var id string
		if err := conn.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, requested_by, subject_user_id, subject_name, subject_email, flow_id, flow_name,
			 link_expires_at, status, completed_at, purged_at, purge_at, retention_override_seconds)
			VALUES ($1, $2, $2, 'Sam', 'sam@example.org', 'f1', 'Passport', now() + interval '10 minutes',
				$3, $4, $5, $6, $7)
			RETURNING id`, orgID, userID, status, completedAt, purgedAt, purgeAt, overrideSeconds).Scan(&id); err != nil {
			t.Fatalf("insert %s request: %v", status, err)
		}
		return id
	}
	week := int((7 * 24 * time.Hour).Seconds())
	approved := insert("approved", &completed, nil, nil, nil)
	running := insert("pending", nil, nil, nil, nil)
	purged := insert("approved", &completed, &completed, nil, nil)
	onFlow := insert("approved", &completed, nil, &flowPurge, &week)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	wantApproved := completed.Add(memberRetention)
	for id, want := range map[string]*time.Time{approved: &wantApproved, purged: nil, onFlow: &flowPurge} {
		var got *time.Time
		if err := conn.QueryRow(ctx, `SELECT purge_at FROM identity_proofing_requests WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("read purge time: %v", err)
		}
		if (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
			t.Errorf("request %s purge time = %v, want %v", id, got, want)
		}
	}
	// An unstarted link lapses at link_expires_at, its purge time a retention on.
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT purge_at = link_expires_at + $2::interval FROM identity_proofing_requests
		WHERE id = $1`, running, memberRetention.String()).Scan(&ok); err != nil || !ok {
		t.Errorf("unstarted request purge time is its link's lapse plus the retention = %v, %v", ok, err)
	}
}
