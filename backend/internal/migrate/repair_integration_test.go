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

// memberIdentityVersion is 20260716150000_add_member_identity.sql. It was
// rewritten in place after it had been applied; legacyMemberIdentitySQL is the
// version databases migrated before the rewrite actually ran (commit 65e505f).
const memberIdentityVersion int64 = 20260716150000

const legacyMemberIdentitySQL = `
ALTER TABLE memberships
    ADD COLUMN phone             TEXT,
    ADD COLUMN identity_verified BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE identity_reviews
    ADD COLUMN phone TEXT;
`

// TestUpRepairsLegacyMemberIdentitySchema reproduces a database that applied
// the pre-rewrite version of 20260716150000 and checks that migrating to head
// converges it on the shape the rewritten file (and the stores) expect, with the
// verification timestamp backfilled from the membership's creation.
func TestUpRepairsLegacyMemberIdentitySchema(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)

	if err := migrate.UpTo(ctx, dsn, memberIdentityVersion-1); err != nil {
		t.Fatalf("migrate up to before %d: %v", memberIdentityVersion, err)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, legacyMemberIdentitySQL); err != nil {
		t.Fatalf("apply legacy migration: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`,
		memberIdentityVersion,
	); err != nil {
		t.Fatalf("record legacy migration as applied: %v", err)
	}

	verifiedAt := time.Date(2026, time.August, 20, 9, 30, 0, 0, time.UTC)
	verified, unverified := seedLegacyMembers(t, ctx, conn, verifiedAt)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up from legacy schema: %v", err)
	}

	assertColumn(t, ctx, conn, "memberships", "identity_verified", false)
	assertColumn(t, ctx, conn, "memberships", "identity_verified_at", true)
	assertColumn(t, ctx, conn, "memberships", "date_of_birth", true)
	assertColumn(t, ctx, conn, "identity_reviews", "date_of_birth", true)

	var got *time.Time
	if err := conn.QueryRow(ctx,
		`SELECT identity_verified_at FROM memberships WHERE user_id = $1`, verified,
	).Scan(&got); err != nil {
		t.Fatalf("read verified member: %v", err)
	}
	if got == nil || !got.Equal(verifiedAt) {
		t.Errorf("verified member identity_verified_at = %v, want %v (its created_at)", got, verifiedAt)
	}

	if err := conn.QueryRow(ctx,
		`SELECT identity_verified_at FROM memberships WHERE user_id = $1`, unverified,
	).Scan(&got); err != nil {
		t.Fatalf("read unverified member: %v", err)
	}
	if got != nil {
		t.Errorf("unverified member identity_verified_at = %v, want NULL", got)
	}
}

// TestUpIsIdempotentAfterRepair applies the whole history to a fresh database
// twice: the second run must find nothing to do and the repair's guards must
// not have altered the shape the rewritten 20260716150000 produced.
func TestUpIsIdempotentAfterRepair(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)

	for i := range 2 {
		if err := migrate.Up(ctx, dsn); err != nil {
			t.Fatalf("migrate up (run %d): %v", i+1, err)
		}
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	assertColumn(t, ctx, conn, "memberships", "identity_verified", false)
	assertColumn(t, ctx, conn, "memberships", "identity_verified_at", true)
	assertColumn(t, ctx, conn, "memberships", "date_of_birth", true)
	assertColumn(t, ctx, conn, "identity_reviews", "date_of_birth", true)
}

// seedLegacyMembers inserts one organisation with two members on the legacy
// schema: one flagged identity_verified with a known created_at, one not.
func seedLegacyMembers(t *testing.T, ctx context.Context, conn *pgx.Conn, verifiedAt time.Time) (verified, unverified string) {
	t.Helper()

	var orgID string
	if err := conn.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Repair Test BV', 'repair-test', '12345678', 'NLNHR.12345678', 'repair-test@qerds.test')
		RETURNING id`,
	).Scan(&orgID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	insert := func(email string, isVerified bool) string {
		var userID string
		if err := conn.QueryRow(ctx, `
			INSERT INTO users (email, given_names, last_name)
			VALUES ($1, 'Test', 'Member')
			RETURNING id`, email,
		).Scan(&userID); err != nil {
			t.Fatalf("insert user %s: %v", email, err)
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO memberships (organization_id, user_id, identity_verified, created_at)
			VALUES ($1, $2, $3, $4)`,
			orgID, userID, isVerified, verifiedAt,
		); err != nil {
			t.Fatalf("insert membership %s: %v", email, err)
		}
		return userID
	}

	return insert("verified@example.com", true), insert("unverified@example.com", false)
}

func assertColumn(t *testing.T, ctx context.Context, conn *pgx.Conn, table, column string, want bool) {
	t.Helper()

	var got bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
		)`, table, column,
	).Scan(&got); err != nil {
		t.Fatalf("inspect %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Errorf("%s.%s exists = %t, want %t", table, column, got, want)
	}
}
