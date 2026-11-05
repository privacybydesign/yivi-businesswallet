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

// reviewItemsVersion is 20261105120000_create_identity_proofing_review_items.sql
// and addressItemsVersion the migration that moves the address reviews onto it.
const (
	reviewItemsVersion  int64 = 20261105120000
	addressItemsVersion int64 = 20261105120100
)

// TestUpMovesAddressReviewsToItems fills the address review columns with a
// pending, a confirmed and no review, then checks that each review became one
// address item, due its request's retention after the address was entered.
func TestUpMovesAddressReviewsToItems(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, reviewItemsVersion-1); err != nil {
		t.Fatalf("migrate up to before the review items: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID, reviewer string
	seedScan(t, conn, &orgID, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('acme', 'acme', 'acme', 'acme', 'acme@qerds.test') RETURNING id`)
	seedScan(t, conn, &reviewer, `INSERT INTO users (email, given_names, last_name) VALUES ('rita@example.org', 'Rita', 'Reviewer') RETURNING id`)
	added := time.Date(2026, time.October, 30, 9, 0, 0, 0, time.UTC)
	decidedAt := added.Add(time.Hour)
	request := func(review *string) string {
		t.Helper()
		var id string
		seedScan(t, conn, &id, `INSERT INTO identity_proofing_requests
				(organization_id, subject_kind, subject_name, subject_email, flow_id, flow_name, link_expires_at,
				 address_added_at, address_review, address_reviewed_by, address_reviewed_at)
			VALUES ($1, 'external', 'Anna', 'anna@example.org', 'f', 'F', now(), $2, $3,
				CASE WHEN $3 = 'confirmed' THEN $4::uuid END, CASE WHEN $3 = 'confirmed' THEN $5::timestamptz END)
			RETURNING id`, orgID, added, review, reviewer, decidedAt)
		return id
	}
	pendingState, confirmedState := "pending", "confirmed"
	pending, confirmed, none := request(&pendingState), request(&confirmedState), request(nil)

	if err := migrate.UpTo(ctx, dsn, addressItemsVersion); err != nil {
		t.Fatalf("migrate up to the address items: %v", err)
	}
	type item struct {
		state     string
		createdAt time.Time
		dueAt     time.Time
		decidedBy *string
		decidedAt *time.Time
	}
	read := func(requestID string) []item {
		t.Helper()
		rows, err := conn.Query(ctx, `SELECT state, created_at, due_at, decided_by::text, decided_at
			FROM identity_proofing_review_items WHERE request_id = $1 AND kind = 'address'`, requestID)
		if err != nil {
			t.Fatalf("read items: %v", err)
		}
		out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
			var it item
			err := row.Scan(&it.state, &it.createdAt, &it.dueAt, &it.decidedBy, &it.decidedAt)
			return it, err
		})
		if err != nil {
			t.Fatalf("read items: %v", err)
		}
		return out
	}
	// An org without settings keeps an external person's data a week.
	wantDue := added.Add(7 * 24 * time.Hour)
	if got := read(pending); len(got) != 1 || got[0].state != pendingState || !got[0].createdAt.Equal(added) ||
		!got[0].dueAt.Equal(wantDue) || got[0].decidedAt != nil {
		t.Errorf("pending items = %+v; want one pending since %v, due %v", got, added, wantDue)
	}
	if got := read(confirmed); len(got) != 1 || got[0].state != confirmedState || got[0].decidedBy == nil ||
		*got[0].decidedBy != reviewer || got[0].decidedAt == nil || !got[0].decidedAt.Equal(decidedAt) {
		t.Errorf("confirmed items = %+v; want one confirmed by %s at %v", got, reviewer, decidedAt)
	}
	if got := read(none); len(got) != 0 {
		t.Errorf("unreviewed items = %+v; want none", got)
	}
	var columns int
	seedScan(t, conn, &columns, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'identity_proofing_requests' AND column_name LIKE 'address_review%'`)
	if columns != 0 {
		t.Errorf("%d address_review columns left; want none", columns)
	}
}
