//go:build integration

package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// foreignKeyViolation is Postgres's SQLSTATE for a foreign key violation.
const foreignKeyViolation = "23503"

// A row that names a proofing request must name one of its own org: the
// composite foreign keys refuse another tenant's request.
func TestRequestChildrenStayInOrg(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	request := func(slug string) (orgID, requestID string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, 'NLNHR.' || $1, $1 || '@qerds.test') RETURNING id`, slug).Scan(&orgID); err != nil {
			t.Fatalf("insert org %s: %v", slug, err)
		}

		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, '', 'subject@example.test', 'flow', 'Flow', now()) RETURNING id`, orgID).Scan(&requestID); err != nil {
			t.Fatalf("insert request %s: %v", slug, err)
		}
		return orgID, requestID
	}
	orgA, requestA := request("tenant-a")
	orgB, requestB := request("tenant-b")

	apiKey := func(orgID string) (keyID string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `WITH customer AS (
				INSERT INTO identity_proofing_customers (organization_id, name) VALUES ($1, 'Customer') RETURNING id)
			INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			SELECT $1, id, 'Key', 'prefix', gen_random_uuid()::text::bytea FROM customer RETURNING id`, orgID).Scan(&keyID); err != nil {
			t.Fatalf("insert api key: %v", err)
		}
		return keyID
	}
	keyA, keyB := apiKey(orgA), apiKey(orgB)

	for name, insert := range map[string]string{
		"match": `INSERT INTO identity_proofing_request_matches (organization_id, request_id, matched_request_id, level)
			VALUES ($1, $2, $3, 'strong')`,
		"diploma": `INSERT INTO identity_proofing_request_diplomas
			(organization_id, request_id, document_type, qualification, institution, place_of_issue, date_awarded, document_number)
			VALUES ($1, $3, 'Diploma', 'Q', 'I', 'P', '2020-01-01', $2::text)`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pool.Exec(ctx, insert, orgA, requestA, requestB)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
				t.Errorf("a %s of org A naming org B's request = %v, want a foreign key violation", name, err)
			}
		})
	}

	if _, err := pool.Exec(ctx, `INSERT INTO identity_proofing_request_matches (organization_id, request_id, matched_request_id, level)
		VALUES ($1, $2, $2, 'strong')`, orgA, requestA); err == nil {
		t.Error("a request matched with itself was accepted")
	}

	t.Run("api key", func(t *testing.T) {
		const setKey = `UPDATE identity_proofing_requests SET api_key_id = $2 WHERE id = $1`
		_, err := pool.Exec(ctx, setKey, requestA, keyB)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != foreignKeyViolation {
			t.Errorf("a request of org A naming org B's api key = %v, want a foreign key violation", err)
		}

		if _, err := pool.Exec(ctx, setKey, requestA, keyA); err != nil {
			t.Fatalf("set org A's own api key: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM identity_proofing_api_keys WHERE id = $1`, keyA); err != nil {
			t.Fatalf("delete api key: %v", err)
		}
		var keyID, orgID *string
		if err := pool.QueryRow(ctx, `SELECT api_key_id::text, organization_id::text FROM identity_proofing_requests WHERE id = $1`,
			requestA).Scan(&keyID, &orgID); err != nil {
			t.Fatalf("read request: %v", err)
		}
		if keyID != nil || orgID == nil || *orgID != orgA {
			t.Errorf("after its key is deleted the request has key %v, org %v; want no key, org %s", keyID, orgID, orgA)
		}
	})
}
