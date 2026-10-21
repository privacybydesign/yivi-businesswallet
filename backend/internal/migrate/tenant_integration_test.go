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

// tenantRow is one org's proofing request and API key.
type tenantRow struct {
	orgID, requestID, keyID string
}

// A row that names a proofing request or API key must name one of its own org:
// the composite foreign keys refuse another tenant's.
func TestRequestChildrenStayWithOrg(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	org := func(slug string) tenantRow {
		t.Helper()
		row := tenantRow{}
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, 'NLNHR.' || $1, $1 || '@qerds.test') RETURNING id`, slug).Scan(&row.orgID); err != nil {
			t.Fatalf("insert org %s: %v", slug, err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_api_keys (organization_id, name, prefix, secret_hash)
			VALUES ($1, 'Key', 'prefix', gen_random_uuid()::text::bytea) RETURNING id`,
			row.orgID).Scan(&row.keyID); err != nil {
			t.Fatalf("insert api key: %v", err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_kind, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, 'external', '', 'subject@example.test', 'flow', 'Flow', now()) RETURNING id`,
			row.orgID).Scan(&row.requestID); err != nil {
			t.Fatalf("insert request: %v", err)
		}
		return row
	}
	a, otherOrg := org("tenant-a"), org("tenant-b")

	children := map[string]string{
		"match": `INSERT INTO identity_proofing_request_matches
			(organization_id, request_id, matched_request_id, level)
			VALUES ($1, $2, $3, 'strong')`,
		"match check": `INSERT INTO identity_proofing_request_match_checks
			(organization_id, request_id, candidate_request_id, level)
			VALUES ($1, $2, $3, 'strong')`,
		"webhook delivery": `INSERT INTO identity_proofing_webhook_deliveries
			(organization_id, event, request_id, payload, endpoint_url)
			VALUES ($1, 'session.approved', $3, jsonb_build_object('from', $2::text), 'https://org.example.test/hook')`,
		"idempotency key": `INSERT INTO identity_proofing_idempotency_keys
			(organization_id, key, request_hash, request_id)
			VALUES ($1, $2::text, '\x00', $3)`,
	}
	for name, insert := range children {
		t.Run(name+" of another org", func(t *testing.T) {
			_, err := pool.Exec(ctx, insert, a.orgID, a.requestID, otherOrg.requestID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
				t.Errorf("a %s naming another org's request = %v, want a foreign key violation", name, err)
			}
		})
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
		(organization_id, request_id, matched_request_id, level)
		VALUES ($1, $2, $2, 'strong')`, a.orgID, a.requestID); err == nil {
		t.Error("a request matched with itself was accepted")
	}

	t.Run("api key", func(t *testing.T) {
		const setKey = `UPDATE identity_proofing_requests SET api_key_id = $2 WHERE id = $1`
		_, err := pool.Exec(ctx, setKey, a.requestID, otherOrg.keyID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
			t.Errorf("a request naming another org's api key = %v, want a foreign key violation", err)
		}

		var memberRequest string
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, '', 'member@example.test', 'flow', 'Flow', now()) RETURNING id`, a.orgID).Scan(&memberRequest); err != nil {
			t.Fatalf("insert member request: %v", err)
		}
		_, err = pool.Exec(ctx, setKey, memberRequest, a.keyID)
		if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
			t.Errorf("a member's request naming an api key = %v, want a check violation", err)
		}

		if _, err := pool.Exec(ctx, setKey, a.requestID, a.keyID); err != nil {
			t.Fatalf("set the org's own api key: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM identity_proofing_api_keys WHERE id = $1`, a.keyID); err != nil {
			t.Fatalf("delete api key: %v", err)
		}
		var keyID, kind *string
		if err := pool.QueryRow(ctx, `SELECT api_key_id::text, subject_kind FROM identity_proofing_requests WHERE id = $1`,
			a.requestID).Scan(&keyID, &kind); err != nil {
			t.Fatalf("read request: %v", err)
		}
		if keyID != nil || kind == nil || *kind != "external" {
			t.Errorf("after its key is deleted the request has key %v, kind %v; want no key, kind external", keyID, kind)
		}
	})
}
