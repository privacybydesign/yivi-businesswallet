//go:build integration

package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// uniqueViolation is Postgres's SQLSTATE for a unique constraint violation.
const uniqueViolation = "23505"

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// A purged request keeps none of its subject's personal data, a provider
// session belongs to one request, and the closed-set columns take only their
// values.
func TestProofingRequestConstraints(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Constraint Test BV', 'constraint-test', '87654321', 'NLNHR.87654321', 'constraint-test@qerds.test')
		RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	newRequest := func(t *testing.T) (id string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_requests
			(organization_id, subject_name, subject_email, flow_id, flow_name, link_expires_at)
			VALUES ($1, 'Anna', 'subject@example.test', 'flow', 'Flow', now()) RETURNING id`, orgID).Scan(&id); err != nil {
			t.Fatalf("insert request: %v", err)
		}
		return id
	}

	for _, tc := range []struct {
		name, update string
	}{
		{"purged with the subject's e-mail kept", `UPDATE identity_proofing_requests SET purged_at = now(), subject_name = '' WHERE id = $1`},
		{"purged with the employment kept", `UPDATE identity_proofing_requests
			SET purged_at = now(), subject_name = '', subject_email = '', employment_ciphertext = '\x00' WHERE id = $1`},
		{"purged with the registered address kept", `UPDATE identity_proofing_requests
			SET purged_at = now(), subject_name = '', subject_email = '', registered_address_ciphertext = '\x00' WHERE id = $1`},
		{"unknown method", `UPDATE identity_proofing_requests SET method = 'carrier_pigeon' WHERE id = $1`},
		{"unknown eIDAS level", `UPDATE identity_proofing_requests SET eidas_level = 'medium' WHERE id = $1`},
		{"unknown required level", `UPDATE identity_proofing_requests SET required_assurance_level = 'medium' WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.update, newRequest(t)); sqlState(err) != checkViolation {
				t.Errorf("update = %v, want a check violation", err)
			}
		})
	}

	t.Run("written to after the purge", func(t *testing.T) {
		id := newRequest(t)
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests
			SET purged_at = now(), subject_name = '', subject_email = '' WHERE id = $1`, id); err != nil {
			t.Fatalf("purge as RequestStore.Purge leaves it: %v", err)
		}
		_, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET redirect_url = 'https://customer.example.test/back' WHERE id = $1`, id)
		if sqlState(err) != checkViolation {
			t.Errorf("a redirect written after the purge = %v, want a check violation", err)
		}
	})

	t.Run("one provider session for two requests", func(t *testing.T) {
		const setSession = `UPDATE identity_proofing_requests
			SET ips_session_id = 'ses_shared', ips_session_token_ciphertext = '\x00' WHERE id = $1`
		if _, err := pool.Exec(ctx, setSession, newRequest(t)); err != nil {
			t.Fatalf("attach the session: %v", err)
		}
		if _, err := pool.Exec(ctx, setSession, newRequest(t)); sqlState(err) != uniqueViolation {
			t.Errorf("attach it to a second request = %v, want a unique violation", err)
		}
	})

	t.Run("engine session status", func(t *testing.T) {
		const insert = `INSERT INTO identity_proofing_sessions (id, organization_id, token_hash, status, expires_at, data)
			VALUES ($1, $2, $1, $3, now(), '\x00')`
		if _, err := pool.Exec(ctx, insert, "ses_created", orgID, "created"); err != nil {
			t.Fatalf("insert a created session: %v", err)
		}
		if _, err := pool.Exec(ctx, insert, "ses_paused", orgID, "paused"); sqlState(err) != checkViolation {
			t.Errorf("insert a paused session = %v, want a check violation", err)
		}
	})
}
