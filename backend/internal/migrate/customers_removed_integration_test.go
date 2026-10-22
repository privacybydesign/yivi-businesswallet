//go:build integration

package migrate_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// lastCustomerVersion is 20261019120000_add_address_to_identity_proofing_purged_check.sql,
// the last migration before identity proofing lost its customers.
const lastCustomerVersion int64 = 20261019120000

// keysReachOtherCustomers is the query .ai/features/identity-proofing.md gives
// to run before the migration: the orgs where an API key that keeps working
// reaches sessions of customers it was not issued to, as it reaches every
// external session of its org.
const keysReachOtherCustomers = `SELECT c.organization_id
FROM identity_proofing_customers c
GROUP BY c.organization_id
HAVING count(*) > 1
   AND EXISTS (SELECT 1
               FROM identity_proofing_api_keys k
                        JOIN identity_proofing_customers kc ON kc.id = k.customer_id
               WHERE k.organization_id = c.organization_id
                 AND k.revoked_at IS NULL
                 AND kc.paused_at IS NULL)`

// customerOrgs are the orgs seedCustomers fills, by id.
type customerOrgs struct {
	// acme has two customers that differ in everything: the older one paused,
	// with its endpoint moved to another URL, the newer one not.
	acme string
	// halted has two customers, both paused.
	halted string
	// self paused its own proofing; other was paused by a platform admin.
	self, other string
	// platformAdmin paused other; pausedAt is when halted's last customer was
	// paused, and selfPausedAt when self paused itself.
	platformAdmin          string
	pausedAt, selfPausedAt time.Time
}

// TestUpFoldsCustomersIntoOrg applies the history up to the last one with
// customers, fills it with customers' data in several orgs, and checks that
// what the customers held is the org's afterwards: their settings merged, every
// key (a paused customer's revoked), one webhook endpoint with what still waits
// for it, one default flow, the pauses, and every request, member and external,
// where it was, with its matches. The down migrations then rebuild a customer
// for the org.
func TestUpFoldsCustomersIntoOrg(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, lastCustomerVersion); err != nil {
		t.Fatalf("migrate up to the last customer version: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	orgs := seedCustomers(t, ctx, conn)

	// Of the orgs with more than one customer, only one keeps a working key.
	rows, err := conn.Query(ctx, keysReachOtherCustomers)
	if err != nil {
		t.Fatalf("find the keys that reach other customers: %v", err)
	}
	flagged, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !slices.Equal(flagged, []string{orgs.acme}) {
		t.Errorf("orgs whose keys reach other customers = %v, %v; want only %s", flagged, err, orgs.acme)
	}

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	// Where the customers differ the org takes the shortest lifetime and
	// retention, review for erasure and every origin; the look is the older
	// one's.
	var ttl, retention int
	var displayName, erasure, secondary, text, font string
	var origins []string
	if err := conn.QueryRow(ctx, `SELECT session_ttl_seconds, data_retention_days, erasure_handling, display_name,
			secondary_color, text_color, font_family, allowed_redirect_origins
		FROM identity_proofing_settings WHERE organization_id = $1`, orgs.acme).
		Scan(&ttl, &retention, &erasure, &displayName, &secondary, &text, &font, &origins); err != nil {
		t.Fatalf("read the org's settings: %v", err)
	}
	wantOrigins := []string{"https://new.example", "https://old.example", "https://shared.example"}
	if ttl != 120 || retention != 7 || erasure != "review" || !slices.Equal(origins, wantOrigins) {
		t.Errorf("settings = %d s, %d days, %q, %v; want the shortest, review and every origin %v",
			ttl, retention, erasure, origins, wantOrigins)
	}
	if displayName != "Old Brand" || secondary != "#112233" || text != "#222222" || font != "Open Sans, sans-serif" {
		t.Errorf("look = %q, %q, %q, %q; want the oldest customer's", displayName, secondary, text, font)
	}
	// Where they agree the org keeps it.
	assertCount(t, ctx, conn, "the halted org's agreed erasure", 1,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id = $1 AND erasure_handling = 'automatic'`, orgs.halted)
	assertCount(t, ctx, conn, "settings of orgs without customers", 0,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id IN ($1, $2)`, orgs.self, orgs.other)

	// A paused customer's keys are revoked; the others' keep working.
	assertCount(t, ctx, conn, "api keys of the org", 2,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE organization_id = $1`, orgs.acme)
	assertCount(t, ctx, conn, "working keys of the org", 1,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE organization_id = $1 AND revoked_at IS NULL AND name = 'key-new'`, orgs.acme)
	assertCount(t, ctx, conn, "working keys of the halted org", 0,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE organization_id = $1 AND revoked_at IS NULL`, orgs.halted)

	var url string
	if err := conn.QueryRow(ctx, `SELECT url FROM identity_proofing_webhooks WHERE organization_id = $1`, orgs.acme).Scan(&url); err != nil || url != "https://old.example/v2" {
		t.Errorf("webhook = %q, %v; want the oldest customer's endpoint", url, err)
	}
	assertCount(t, ctx, conn, "webhooks", 1, `SELECT count(*) FROM identity_proofing_webhooks`)

	// What waited for the newer customer is dropped, also the one that named
	// the kept URL, so none goes out under the older one's secret. The older
	// one's stays, though it was queued before its URL moved, and so does the
	// delivered one.
	assertCount(t, ctx, conn, "deliveries left", 2, `SELECT count(*) FROM identity_proofing_webhook_deliveries`)
	assertCount(t, ctx, conn, "the older customer's waiting delivery", 1,
		`SELECT count(*) FROM identity_proofing_webhook_deliveries WHERE status = 'pending' AND endpoint_url = 'https://old.example/hook'`)
	assertCount(t, ctx, conn, "deliveries waiting", 1,
		`SELECT count(*) FROM identity_proofing_webhook_deliveries WHERE status = 'pending'`)

	// The customer's default wins over the members' one: that of the customer
	// whose key still works, though it is the newer one. A data request flow
	// is passed over.
	for _, c := range []struct{ org, want string }{{orgs.acme, "f-default"}, {orgs.halted, "f-id"}} {
		var def string
		if err := conn.QueryRow(ctx, `SELECT flow_id FROM identity_proofing_flow_settings WHERE organization_id = $1 AND is_default`, c.org).Scan(&def); err != nil || def != c.want {
			t.Errorf("default flow = %q, %v; want %q", def, err, c.want)
		}
	}
	assertCount(t, ctx, conn, "the members' flow, no longer the default", 1,
		`SELECT count(*) FROM identity_proofing_flow_settings WHERE flow_id = 'f-member' AND member_allowed AND NOT is_default`)

	assertCount(t, ctx, conn, "member requests", 1, `SELECT count(*) FROM identity_proofing_requests WHERE subject_kind = 'member'`)
	assertCount(t, ctx, conn, "external requests", 3, `SELECT count(*) FROM identity_proofing_requests WHERE subject_kind = 'external'`)
	assertCount(t, ctx, conn, "kept idempotency keys", 0, `SELECT count(*) FROM identity_proofing_idempotency_keys`)
	assertCount(t, ctx, conn, "requests with their key", 1, `SELECT count(*) FROM identity_proofing_requests WHERE api_key_id IS NOT NULL`)

	// A data request's matches stay; the identity match keys hashed the
	// customer, so they are cleared.
	assertCount(t, ctx, conn, "matches", 1, `SELECT count(*) FROM identity_proofing_request_matches`)
	assertCount(t, ctx, conn, "match checks", 1, `SELECT count(*) FROM identity_proofing_request_match_checks`)
	assertCount(t, ctx, conn, "identity match keys", 0, `SELECT count(*) FROM identity_proofing_requests WHERE identity_match_key IS NOT NULL`)

	// What was paused stays paused, as the platform's: an org that paused
	// itself since it did, one whose every customer was paused since the last
	// was. One paused customer of several pauses nothing. A platform admin's
	// pause keeps who set it.
	assertCount(t, ctx, conn, "a pause of the org with one customer paused", 0,
		`SELECT count(*) FROM identity_proofing_org_pauses WHERE organization_id = $1`, orgs.acme)
	for _, c := range []struct {
		name, org string
		since     time.Time
	}{{"the halted org", orgs.halted, orgs.pausedAt}, {"the org that paused itself", orgs.self, orgs.selfPausedAt}} {
		var since time.Time
		var by *string
		if err := conn.QueryRow(ctx, `SELECT platform_paused_at, platform_paused_by::text FROM identity_proofing_org_pauses
			WHERE organization_id = $1`, c.org).Scan(&since, &by); err != nil || !since.Equal(c.since) || by != nil {
			t.Errorf("%s's pause = %v by %v, %v; want the platform's since %v, by nobody", c.name, since, by, err, c.since)
		}
	}
	assertCount(t, ctx, conn, "the platform's pause", 1,
		`SELECT count(*) FROM identity_proofing_org_pauses WHERE organization_id = $1 AND platform_paused_by = $2`,
		orgs.other, orgs.platformAdmin)

	for _, table := range []string{"identity_proofing_customers", "identity_proofing_customer_flows"} {
		assertCount(t, ctx, conn, table+" tables left", 0,
			`SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, table)
	}

	// Back to the last customer version, whatever was added after it since.
	for {
		version, err := migrate.Version(ctx, dsn)
		if err != nil {
			t.Fatalf("read the version: %v", err)
		}
		if version <= lastCustomerVersion {
			break
		}
		if err := migrate.Down(ctx, dsn); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
	}
	assertCount(t, ctx, conn, "customers rebuilt for the org", 1,
		`SELECT count(*) FROM identity_proofing_customers WHERE organization_id = $1`, orgs.acme)
	assertCount(t, ctx, conn, "keys with a customer", 4,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE customer_id IS NOT NULL`)
	assertCount(t, ctx, conn, "keys still revoked", 3,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE revoked_at IS NOT NULL`)
	assertCount(t, ctx, conn, "customers with the settings", 1,
		`SELECT count(*) FROM identity_proofing_customers WHERE organization_id = $1 AND erasure_handling = 'review'
			AND session_ttl_seconds = 120 AND data_retention_days = 7 AND cardinality(allowed_redirect_origins) = 3
			AND secondary_color = '#112233' AND text_color = '#222222' AND font_family = 'Open Sans, sans-serif'`, orgs.acme)
	assertCount(t, ctx, conn, "the customer's default", 1,
		`SELECT count(*) FROM identity_proofing_customer_flows WHERE organization_id = $1 AND is_default AND flow_id = 'f-default'`, orgs.acme)
	assertCount(t, ctx, conn, "pauses kept", 3,
		`SELECT count(*) FROM identity_proofing_org_pauses WHERE platform_paused_at IS NOT NULL AND org_paused_at IS NULL`)
	assertCount(t, ctx, conn, "external requests with a customer", 3,
		`SELECT count(*) FROM identity_proofing_requests WHERE customer_id IS NOT NULL`)
	assertCount(t, ctx, conn, "matches with a customer", 1,
		`SELECT count(*) FROM identity_proofing_request_matches WHERE customer_id IS NOT NULL`)
	assertCount(t, ctx, conn, "member requests with a customer", 0,
		`SELECT count(*) FROM identity_proofing_requests WHERE customer_id IS NULL AND subject_user_id IS NULL`)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	assertCount(t, ctx, conn, "external requests after up again", 3,
		`SELECT count(*) FROM identity_proofing_requests WHERE subject_kind = 'external'`)
	assertCount(t, ctx, conn, "the org's default after up again", 1,
		`SELECT count(*) FROM identity_proofing_flow_settings WHERE organization_id = $1 AND is_default AND flow_id = 'f-default'`, orgs.acme)
}

// seedCustomers fills the schema before customers were removed, as
// customerOrgs describes, and returns the orgs.
func seedCustomers(t *testing.T, ctx context.Context, conn *pgx.Conn) customerOrgs {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	scan := func(dst any, sql string, args ...any) {
		t.Helper()
		if err := conn.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	org := func(slug, kvk string) string {
		t.Helper()
		var id string
		scan(&id, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $2, 'NLNHR.' || $2, $1 || '@qerds.test') RETURNING id`, slug, kvk)
		return id
	}
	user := func(email string) string {
		t.Helper()
		var id string
		scan(&id, `INSERT INTO users (email, given_names, last_name) VALUES ($1, 'Mia', 'Member') RETURNING id`, email)
		return id
	}

	var orgs customerOrgs
	orgs.acme, orgs.halted = org("acme", "12345678"), org("halted", "23456789")
	orgs.self, orgs.other = org("self", "34567890"), org("other", "87654321")
	userID, adminID := user("m@example.org"), user("admin@example.org")
	orgs.platformAdmin = user("platform@example.org")

	var older, newer string
	scan(&older, `INSERT INTO identity_proofing_customers
			(organization_id, name, session_ttl_seconds, data_retention_days, erasure_handling, display_name,
			 secondary_color, text_color, font_family, allowed_redirect_origins, created_at, paused_at)
		VALUES ($1, 'Older', 300, 90, 'automatic', 'Old Brand', '#112233', '#222222', 'Open Sans, sans-serif',
			ARRAY['https://old.example', 'https://shared.example'], now() - interval '2 days', now() - interval '1 hour')
		RETURNING id`, orgs.acme)
	scan(&newer, `INSERT INTO identity_proofing_customers
			(organization_id, name, session_ttl_seconds, data_retention_days, display_name, allowed_redirect_origins, created_at)
		VALUES ($1, 'Newer', 120, 7, 'New Brand', ARRAY['https://shared.example', 'https://new.example'],
			now() - interval '1 day') RETURNING id`, orgs.acme)

	// The older customer moved its endpoint after a delivery was queued for it.
	for _, c := range []struct{ id, name, url string }{
		{older, "old", "https://old.example/v2"}, {newer, "new", "https://new.example/hook"},
	} {
		exec(`INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			VALUES ($1, $2, $3, $3, $4)`, orgs.acme, c.id, "key-"+c.name, []byte("hash-"+c.name))
		exec(`INSERT INTO identity_proofing_webhooks (customer_id, organization_id, url, events, secret_ciphertext, secret_last4)
			VALUES ($1, $2, $3, ARRAY['session.verified'], $4, 'abcd')`, c.id, orgs.acme, c.url, []byte("sealed"))
	}
	delivery := `INSERT INTO identity_proofing_webhook_deliveries (organization_id, customer_id, event, payload, endpoint_url, status)
		VALUES ($1, $2, 'session.verified', '{}', $3, $4)`
	exec(delivery, orgs.acme, older, "https://old.example/hook", "pending")
	exec(delivery, orgs.acme, newer, "https://new.example/hook", "pending")
	// The newer customer's, last sent to the URL the older one has now.
	exec(delivery, orgs.acme, newer, "https://old.example/v2", "pending")
	exec(delivery, orgs.acme, newer, "https://new.example/hook", "delivered")

	// The members' default, and each customer's: the paused older one's, and
	// the newer one's, whose key works.
	exec(`INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, member_allowed, member_default)
		VALUES ($1, 'f-member', true, true)`, orgs.acme)
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-old', true)`, orgs.acme, older)
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-other', false)`, orgs.acme, older)
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-default', true)`, orgs.acme, newer)

	request := `INSERT INTO identity_proofing_requests
			(organization_id, customer_id, subject_user_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
		VALUES ($1, $2, $3, 'Anna', 'anna@example.org', 'f-default', 'Default', now() + interval '1 hour') RETURNING id`
	var first, second string
	scan(&first, request, orgs.acme, older, nil)
	scan(&second, request, orgs.acme, older, nil)
	exec(request+" ", orgs.acme, newer, nil)
	exec(request+" ", orgs.acme, nil, userID)

	// The older customer's key created one of its requests, which holds the
	// identity match key of its person; a data request matched the other to it.
	exec(`UPDATE identity_proofing_requests SET api_key_id = (SELECT id FROM identity_proofing_api_keys WHERE customer_id = $1 LIMIT 1),
			identity_match_key = '\x0102' WHERE id = $2`, older, first)
	exec(`INSERT INTO identity_proofing_request_matches (organization_id, customer_id, request_id, matched_request_id, level)
		VALUES ($1, $2, $3, $4, 'strong')`, orgs.acme, older, second, first)
	exec(`INSERT INTO identity_proofing_request_match_checks (organization_id, customer_id, request_id, candidate_request_id, level)
		VALUES ($1, $2, $3, $4, 'strong')`, orgs.acme, older, second, first)

	exec(`INSERT INTO identity_proofing_idempotency_keys (organization_id, customer_id, key, request_hash, request_id)
		VALUES ($1, $2, 'k', $3, $4)`, orgs.acme, older, []byte("h"), first)

	// Both of halted's customers are paused, and agree on erasure. The older
	// one's default is a data request flow, which cannot be the org's.
	exec(`INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, kind) VALUES ($1, 'f-data', 'data_access')`, orgs.halted)
	scan(&orgs.pausedAt, `SELECT date_trunc('second', now() - interval '3 hours')`)
	for i, c := range []struct {
		name, flow string
		pausedAt   time.Time
	}{{"First", "f-data", orgs.pausedAt.Add(-time.Hour)}, {"Second", "f-id", orgs.pausedAt}} {
		var id string
		scan(&id, `INSERT INTO identity_proofing_customers (organization_id, name, erasure_handling, created_at, paused_at)
			VALUES ($1, $2, 'automatic', now() - make_interval(days => $3), $4) RETURNING id`,
			orgs.halted, c.name, 2-i, c.pausedAt)
		exec(`INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			VALUES ($1, $2, $3, $3, $4)`, orgs.halted, id, "key-"+c.name, []byte("hash-"+c.name))
		exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default)
			VALUES ($1, $2, $3, true)`, orgs.halted, id, c.flow)
	}

	scan(&orgs.selfPausedAt, `SELECT date_trunc('second', now() - interval '5 hours')`)
	exec(`INSERT INTO identity_proofing_org_pauses (organization_id, org_paused_at, org_paused_by) VALUES ($1, $2, $3)`,
		orgs.self, orgs.selfPausedAt, adminID)
	exec(`INSERT INTO identity_proofing_org_pauses (organization_id, platform_paused_at, platform_paused_by) VALUES ($1, now(), $2)`,
		orgs.other, orgs.platformAdmin)
	return orgs
}

func assertCount(t *testing.T, ctx context.Context, conn *pgx.Conn, what string, want int, sql string, args ...any) {
	t.Helper()
	var got int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if got != want {
		t.Errorf("%s = %d, want %d", what, got, want)
	}
}
