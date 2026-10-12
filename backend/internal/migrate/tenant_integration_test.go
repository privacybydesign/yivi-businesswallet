//go:build integration

package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const (
	// foreignKeyViolation is Postgres's SQLSTATE for a foreign key violation.
	foreignKeyViolation = "23503"
	// checkViolation is Postgres's SQLSTATE for a check constraint violation.
	checkViolation = "23514"
)

// tenantRow is one customer's proofing request and API key, in its org.
type tenantRow struct {
	orgID, customerID, requestID, keyID string
}

// A row that names a proofing request or API key must name one of its own
// customer: the composite foreign keys refuse another tenant's, and another
// customer's in the same org.
func TestRequestChildrenStayWithCustomer(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	org := func(slug string) (orgID string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, 'NLNHR.' || $1, $1 || '@qerds.test') RETURNING id`, slug).Scan(&orgID); err != nil {
			t.Fatalf("insert org %s: %v", slug, err)
		}
		return orgID
	}
	customer := func(orgID string) tenantRow {
		t.Helper()
		row := tenantRow{orgID: orgID}
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_customers (organization_id, name)
			VALUES ($1, gen_random_uuid()::text) RETURNING id`, orgID).Scan(&row.customerID); err != nil {
			t.Fatalf("insert customer: %v", err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			VALUES ($1, $2, 'Key', 'prefix', gen_random_uuid()::text::bytea) RETURNING id`,
			orgID, row.customerID).Scan(&row.keyID); err != nil {
			t.Fatalf("insert api key: %v", err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, customer_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, $2, '', 'subject@example.test', 'flow', 'Flow', now()) RETURNING id`,
			orgID, row.customerID).Scan(&row.requestID); err != nil {
			t.Fatalf("insert request: %v", err)
		}
		return row
	}
	orgA := org("tenant-a")
	a, sameOrg, otherOrg := customer(orgA), customer(orgA), customer(org("tenant-b"))

	children := map[string]string{
		"match": `INSERT INTO identity_proofing_request_matches
			(organization_id, customer_id, request_id, matched_request_id, level)
			VALUES ($1, $2, $3, $4, 'strong')`,
		"match check": `INSERT INTO identity_proofing_request_match_checks
			(organization_id, customer_id, request_id, candidate_request_id, level)
			VALUES ($1, $2, $3, $4, 'strong')`,
		"webhook delivery": `INSERT INTO identity_proofing_webhook_deliveries
			(organization_id, customer_id, event, request_id, payload, endpoint_url)
			VALUES ($1, $2, 'session.approved', $4, jsonb_build_object('from', $3::text), 'https://customer.example.test/hook')`,
		"idempotency key": `INSERT INTO identity_proofing_idempotency_keys
			(organization_id, customer_id, key, request_hash, request_id)
			VALUES ($1, $2, $3::text, '\x00', $4)`,
	}
	for name, insert := range children {
		for owner, other := range map[string]tenantRow{"another org": otherOrg, "another customer": sameOrg} {
			t.Run(name+" of "+owner, func(t *testing.T) {
				_, err := pool.Exec(ctx, insert, a.orgID, a.customerID, a.requestID, other.requestID)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
					t.Errorf("a %s naming %s's request = %v, want a foreign key violation", name, owner, err)
				}
			})
		}
	}

	t.Run("diploma of another org", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO identity_proofing_request_diplomas
			(organization_id, request_id, document_type, qualification, institution, place_of_issue, date_awarded, document_number)
			VALUES ($1, $2, 'Diploma', 'Q', 'I', 'P', '2020-01-01', 'N')`, a.orgID, otherOrg.requestID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
			t.Errorf("a diploma naming another org's request = %v, want a foreign key violation", err)
		}
	})

	if _, err := pool.Exec(ctx, `INSERT INTO identity_proofing_request_matches
		(organization_id, customer_id, request_id, matched_request_id, level)
		VALUES ($1, $2, $3, $3, 'strong')`, a.orgID, a.customerID, a.requestID); err == nil {
		t.Error("a request matched with itself was accepted")
	}

	t.Run("api key", func(t *testing.T) {
		const setKey = `UPDATE identity_proofing_requests SET api_key_id = $2 WHERE id = $1`
		for owner, other := range map[string]tenantRow{"another org": otherOrg, "another customer": sameOrg} {
			_, err := pool.Exec(ctx, setKey, a.requestID, other.keyID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
				t.Errorf("a request naming %s's api key = %v, want a foreign key violation", owner, err)
			}
		}

		var memberRequest string
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, '', 'member@example.test', 'flow', 'Flow', now()) RETURNING id`, a.orgID).Scan(&memberRequest); err != nil {
			t.Fatalf("insert member request: %v", err)
		}
		_, err := pool.Exec(ctx, setKey, memberRequest, a.keyID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
			t.Errorf("a member's request naming an api key = %v, want a check violation", err)
		}

		if _, err := pool.Exec(ctx, setKey, a.requestID, a.keyID); err != nil {
			t.Fatalf("set the customer's own api key: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM identity_proofing_api_keys WHERE id = $1`, a.keyID); err != nil {
			t.Fatalf("delete api key: %v", err)
		}
		var keyID, customerID *string
		if err := pool.QueryRow(ctx, `SELECT api_key_id::text, customer_id::text FROM identity_proofing_requests WHERE id = $1`,
			a.requestID).Scan(&keyID, &customerID); err != nil {
			t.Fatalf("read request: %v", err)
		}
		if keyID != nil || customerID == nil || *customerID != a.customerID {
			t.Errorf("after its key is deleted the request has key %v, customer %v; want no key, customer %s",
				keyID, customerID, a.customerID)
		}
	})
}
