//go:build integration

package migrate_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// lastCustomerVersion is 20261019120000_add_address_to_identity_proofing_purged_check.sql,
// the last migration before identity proofing lost its customers.
const lastCustomerVersion int64 = 20261019120000

// customersRemovedMigrations is how many migrations remove them.
const customersRemovedMigrations = 12

// TestUpFoldsCustomersIntoOrg applies the history up to the last one with
// customers, fills it with two customers' worth of data in one org, and checks
// that what the customers held is the org's afterwards: the oldest customer's
// settings (white-label and erasure handling included), every key, one webhook
// endpoint, one default flow, and every request, member and external, where it
// was, with its matches. The down migrations then rebuild a customer for the
// org.
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

	orgID, otherID := seedCustomers(t, ctx, conn)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	var ttl, retention int
	var displayName, erasure, secondary, text, font string
	var origins []string
	if err := conn.QueryRow(ctx, `SELECT session_ttl_seconds, data_retention_days, erasure_handling, display_name,
			secondary_color, text_color, font_family, allowed_redirect_origins
		FROM identity_proofing_settings WHERE organization_id = $1`, orgID).
		Scan(&ttl, &retention, &erasure, &displayName, &secondary, &text, &font, &origins); err != nil {
		t.Fatalf("read the org's settings: %v", err)
	}
	if ttl != 300 || retention != 90 || erasure != "automatic" || displayName != "Old Brand" ||
		secondary != "#112233" || text != "#222222" || font != "Open Sans, sans-serif" ||
		!slices.Equal(origins, []string{"https://old.example"}) {
		t.Errorf("settings = %d s, %d days, %q, %q, %q, %q, %q, %v; want the oldest customer's",
			ttl, retention, erasure, displayName, secondary, text, font, origins)
	}
	assertCount(t, ctx, conn, "the other org's settings", 0,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id = $1`, otherID)

	assertCount(t, ctx, conn, "api keys of the org", 2,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE organization_id = $1`, orgID)

	var url string
	if err := conn.QueryRow(ctx, `SELECT url FROM identity_proofing_webhooks WHERE organization_id = $1`, orgID).Scan(&url); err != nil || url != "https://old.example/hook" {
		t.Errorf("webhook = %q, %v; want the oldest customer's endpoint", url, err)
	}
	assertCount(t, ctx, conn, "webhooks", 1, `SELECT count(*) FROM identity_proofing_webhooks`)

	// What waited for the newer customer's endpoint is dropped, so it never
	// goes to the older one's URL; the older one's and a delivered one stay.
	assertCount(t, ctx, conn, "deliveries left", 2, `SELECT count(*) FROM identity_proofing_webhook_deliveries`)
	assertCount(t, ctx, conn, "deliveries waiting for the newer endpoint", 0,
		`SELECT count(*) FROM identity_proofing_webhook_deliveries WHERE endpoint_url = 'https://new.example/hook' AND status = 'pending'`)

	var def string
	if err := conn.QueryRow(ctx, `SELECT flow_id FROM identity_proofing_flow_settings WHERE organization_id = $1 AND is_default`, orgID).Scan(&def); err != nil || def != "f-default" {
		t.Errorf("default flow = %q, %v; want the oldest customer's", def, err)
	}

	assertCount(t, ctx, conn, "member requests", 1, `SELECT count(*) FROM identity_proofing_requests WHERE subject_kind = 'member'`)
	assertCount(t, ctx, conn, "external requests", 3, `SELECT count(*) FROM identity_proofing_requests WHERE subject_kind = 'external'`)
	assertCount(t, ctx, conn, "kept idempotency keys", 0, `SELECT count(*) FROM identity_proofing_idempotency_keys`)
	assertCount(t, ctx, conn, "requests with their key", 1, `SELECT count(*) FROM identity_proofing_requests WHERE api_key_id IS NOT NULL`)

	// A data request's matches stay; the identity match keys hashed the
	// customer, so they are cleared.
	assertCount(t, ctx, conn, "matches", 1, `SELECT count(*) FROM identity_proofing_request_matches`)
	assertCount(t, ctx, conn, "match checks", 1, `SELECT count(*) FROM identity_proofing_request_match_checks`)
	assertCount(t, ctx, conn, "identity match keys", 0, `SELECT count(*) FROM identity_proofing_requests WHERE identity_match_key IS NOT NULL`)

	// A pause the org set itself is lifted; a platform admin's stays.
	assertCount(t, ctx, conn, "the org's own pause", 0,
		`SELECT count(*) FROM identity_proofing_org_pauses WHERE organization_id = $1`, orgID)
	assertCount(t, ctx, conn, "the platform's pause", 1,
		`SELECT count(*) FROM identity_proofing_org_pauses WHERE organization_id = $1 AND platform_paused_at IS NOT NULL`, otherID)

	for _, table := range []string{"identity_proofing_customers", "identity_proofing_customer_flows"} {
		assertCount(t, ctx, conn, table+" tables left", 0,
			`SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, table)
	}

	for range customersRemovedMigrations {
		if err := migrate.Down(ctx, dsn); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
	}
	assertCount(t, ctx, conn, "customers rebuilt for the org", 1,
		`SELECT count(*) FROM identity_proofing_customers WHERE organization_id = $1`, orgID)
	assertCount(t, ctx, conn, "keys with a customer", 2,
		`SELECT count(*) FROM identity_proofing_api_keys WHERE customer_id IS NOT NULL`)
	assertCount(t, ctx, conn, "customers with the settings", 1,
		`SELECT count(*) FROM identity_proofing_customers WHERE organization_id = $1 AND erasure_handling = 'automatic'
			AND secondary_color = '#112233' AND text_color = '#222222' AND font_family = 'Open Sans, sans-serif'`, orgID)
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
}

// seedCustomers fills the schema before customers were removed: an org with
// two customers (the older one first) and an org with none but a platform
// pause. It returns the two orgs' ids.
func seedCustomers(t *testing.T, ctx context.Context, conn *pgx.Conn) (orgID, otherID string) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	scan := func(dst *string, sql string, args ...any) {
		t.Helper()
		if err := conn.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}

	scan(&orgID, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', 'acme', '12345678', 'NLNHR.12345678', 'acme@qerds.test') RETURNING id`)
	scan(&otherID, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Other', 'other', '87654321', 'NLNHR.87654321', 'other@qerds.test') RETURNING id`)
	var userID string
	scan(&userID, `INSERT INTO users (email, given_names, last_name) VALUES ('m@example.org', 'Mia', 'Member') RETURNING id`)

	var older, newer string
	scan(&older, `INSERT INTO identity_proofing_customers
			(organization_id, name, session_ttl_seconds, data_retention_days, erasure_handling, display_name,
			 secondary_color, text_color, font_family, allowed_redirect_origins, created_at)
		VALUES ($1, 'Older', 300, 90, 'automatic', 'Old Brand', '#112233', '#222222', 'Open Sans, sans-serif',
			ARRAY['https://old.example'], now() - interval '2 days') RETURNING id`, orgID)
	scan(&newer, `INSERT INTO identity_proofing_customers
			(organization_id, name, session_ttl_seconds, data_retention_days, display_name, allowed_redirect_origins, created_at)
		VALUES ($1, 'Newer', 120, 7, 'New Brand', ARRAY['https://new.example'], now() - interval '1 day') RETURNING id`, orgID)

	for _, c := range []struct{ id, name string }{{older, "old"}, {newer, "new"}} {
		exec(`INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			VALUES ($1, $2, $3, $3, $4)`, orgID, c.id, "key-"+c.name, []byte("hash-"+c.name))
		exec(`INSERT INTO identity_proofing_webhooks (customer_id, organization_id, url, events, secret_ciphertext, secret_last4)
			VALUES ($1, $2, $3, ARRAY['session.verified'], $4, 'abcd')`, c.id, orgID, "https://"+c.name+".example/hook", []byte("sealed"))
		exec(`INSERT INTO identity_proofing_webhook_deliveries (organization_id, customer_id, event, payload, endpoint_url)
			VALUES ($1, $2, 'session.verified', '{}', $3)`, orgID, c.id, "https://"+c.name+".example/hook")
	}
	// A delivered one.
	exec(`INSERT INTO identity_proofing_webhook_deliveries (organization_id, customer_id, event, payload, endpoint_url, status)
		VALUES ($1, $2, 'session.verified', '{}', 'https://new.example/hook', 'delivered')`, orgID, newer)

	// The org has no default of its own; the older customer's becomes it.
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-default', true)`, orgID, older)
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-other', false)`, orgID, older)
	exec(`INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default) VALUES ($1, $2, 'f-other', true)`, orgID, newer)

	request := `INSERT INTO identity_proofing_requests
			(organization_id, customer_id, subject_user_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
		VALUES ($1, $2, $3, 'Anna', 'anna@example.org', 'f-default', 'Default', now() + interval '1 hour') RETURNING id`
	var first, second string
	scan(&first, request, orgID, older, nil)
	scan(&second, request, orgID, older, nil)
	exec(request+" ", orgID, newer, nil)
	exec(request+" ", orgID, nil, userID)

	// The older customer's key created one of its requests, which holds the
	// identity match key of its person; a data request matched the other to it.
	exec(`UPDATE identity_proofing_requests SET api_key_id = (SELECT id FROM identity_proofing_api_keys WHERE customer_id = $1 LIMIT 1),
			identity_match_key = '\x0102' WHERE id = $2`, older, first)
	exec(`INSERT INTO identity_proofing_request_matches (organization_id, customer_id, request_id, matched_request_id, level)
		VALUES ($1, $2, $3, $4, 'strong')`, orgID, older, second, first)
	exec(`INSERT INTO identity_proofing_request_match_checks (organization_id, customer_id, request_id, candidate_request_id, level)
		VALUES ($1, $2, $3, $4, 'strong')`, orgID, older, second, first)

	exec(`INSERT INTO identity_proofing_idempotency_keys (organization_id, customer_id, key, request_hash, request_id)
		VALUES ($1, $2, 'k', $3, $4)`, orgID, older, []byte("h"), first)

	exec(`INSERT INTO identity_proofing_org_pauses (organization_id, org_paused_at) VALUES ($1, now())`, orgID)
	exec(`INSERT INTO identity_proofing_org_pauses (organization_id, platform_paused_at) VALUES ($1, now())`, otherID)
	return orgID, otherID
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
