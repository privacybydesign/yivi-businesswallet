package openid4vppresenter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// consumedRetention is how long a consumed (completed/denied/expired) row is kept
// before the pruner deletes it, so a browser that resumes late is told "already
// used" or "expired" rather than "unknown".
const consumedRetention = time.Hour

// Store persists openid4vp_transactions and writes each state transition's audit
// event in the same transaction.
type Store struct {
	db    database.DB
	audit audit.Recorder
	ttl   time.Duration
}

func NewStore(db database.DB, recorder audit.Recorder, ttl time.Duration) *Store {
	return &Store{db: db, audit: recorder, ttl: ttl}
}

const transactionColumns = `
	id, client_id, request_uri, request_uri_method, verifier_identity, dcql_query,
	nonce, state, response_uri, response_mode, request_object, status, user_id, organization_id,
	expires_at, consumed_at`

func scanTransaction(row pgx.Row) (Transaction, error) {
	var t Transaction
	err := row.Scan(
		&t.ID, &t.ClientID, &t.RequestURI, &t.RequestURIMethod, &t.VerifierIdentity, &t.DCQLQuery,
		&t.Nonce, &t.State, &t.ResponseURI, &t.ResponseMode, &t.RequestObject, &t.Status, &t.UserID, &t.OrganizationID,
		&t.ExpiresAt, &t.ConsumedAt,
	)
	return t, err
}

// NewTransaction is what Create persists: the inbound parameters as received plus
// the validated Request Object.
type NewTransaction struct {
	ClientID         string
	RequestURI       string
	RequestURIMethod string
	Request          RequestObject
}

// Create stores a validated inbound request and returns the opaque id the browser
// carries. The presentation.requested audit event is written pre-auth: no actor
// and no organization yet, and its metadata names only the verifier — never the
// query or the response URI.
func (s *Store) Create(ctx context.Context, in NewTransaction) (string, error) {
	raw, hash, err := newID()
	if err != nil {
		return "", err
	}
	const q = `
		INSERT INTO openid4vp_transactions (
			id_hash, client_id, request_uri, request_uri_method, verifier_identity, dcql_query,
			nonce, state, response_uri, response_mode, request_object, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id`
	expiresAt := time.Now().Add(s.ttl)
	err = database.InTx(ctx, s.db, func(tx database.Querier) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, q,
			hash[:], in.ClientID, in.RequestURI, in.RequestURIMethod, in.Request.VerifierIdentity, in.Request.DCQLQuery,
			in.Request.Nonce, in.Request.State, in.Request.ResponseURI, in.Request.ResponseMode, in.Request.Raw, StatusPendingAuth, expiresAt,
		).Scan(&id); err != nil {
			return fmt.Errorf("openid4vppresenter: create: %w", err)
		}
		return s.audit.Record(ctx, tx, audit.PresentationRequested,
			audit.Target{Type: audit.TargetPresentationTransaction, ID: id.String()},
			audit.Created(map[string]any{"verifier": in.Request.VerifierIdentity}))
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// Get resolves the browser's opaque id to its transaction, expired and consumed
// rows included — the caller reads EffectiveStatus. ErrNotFound for an unknown id.
func (s *Store) Get(ctx context.Context, rawID string) (Transaction, error) {
	hash := hashID(rawID)
	const q = `SELECT ` + transactionColumns + ` FROM openid4vp_transactions WHERE id_hash = $1`
	t, err := scanTransaction(s.db.QueryRow(ctx, q, hash[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("openid4vppresenter: lookup: %w", err)
	}
	return t, nil
}

// BindUser records the authenticated user a pending transaction belongs to. It
// is a no-op when a user is already bound; the caller has compared them first.
func (s *Store) BindUser(ctx context.Context, id, userID uuid.UUID) error {
	const q = `UPDATE openid4vp_transactions SET user_id = $2 WHERE id = $1 AND user_id IS NULL`
	if _, err := s.db.Exec(ctx, q, id, userID); err != nil {
		return fmt.Errorf("openid4vppresenter: bind user: %w", err)
	}
	return nil
}

// SelectOrganization moves a pending, unexpired transaction to org_selected for
// orgID. The WHERE clause is the one-time-use guard: a row that is no longer
// pending is not updated and the call returns ErrNotPending.
func (s *Store) SelectOrganization(ctx context.Context, id, orgID uuid.UUID, orgName string) (Transaction, error) {
	const q = `
		UPDATE openid4vp_transactions
		SET organization_id = $2, status = $3
		WHERE id = $1 AND status = $4 AND consumed_at IS NULL AND expires_at > now()
		RETURNING ` + transactionColumns
	var t Transaction
	err := database.InTx(ctx, s.db, func(tx database.Querier) error {
		var err error
		t, err = scanTransaction(tx.QueryRow(ctx, q, id, orgID, StatusOrgSelected, StatusPendingAuth))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotPending
		}
		if err != nil {
			return fmt.Errorf("openid4vppresenter: select organization: %w", err)
		}
		return s.audit.Record(ctx, tx, audit.PresentationOrgSelected,
			audit.Target{Type: audit.TargetPresentationTransaction, ID: t.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"verifier": t.VerifierIdentity, "organization": orgName}))
	})
	if err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// GetPendingForOrg resolves an org-scoped presentation transaction waiting on
// the governance layer (#113), by its row id — not the opaque, client-facing id
// Get resolves. ErrNotPending covers a transaction that belongs to a different
// organization, was never selected, or has already been decided or expired: the
// same answer for all four, so an admin cannot use it to probe another
// organization's queue or learn a decided transaction's outcome this way.
func (s *Store) GetPendingForOrg(ctx context.Context, orgID, id uuid.UUID) (Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM openid4vp_transactions
		WHERE id = $1 AND organization_id = $2 AND status = $3 AND consumed_at IS NULL AND expires_at > now()`
	t, err := scanTransaction(s.db.QueryRow(ctx, q, id, orgID, StatusOrgSelected))
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotPending
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("openid4vppresenter: get pending %s org %s: %w", id, orgID, err)
	}
	return t, nil
}

// ListPendingForOrg returns the organization's presentation transactions
// waiting on the governance layer: a member selected this org, and now an admin
// must approve or deny before anything reaches the verifier.
func (s *Store) ListPendingForOrg(ctx context.Context, orgID uuid.UUID) ([]Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM openid4vp_transactions
		WHERE organization_id = $1 AND status = $2 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY expires_at ASC`
	rows, err := s.db.Query(ctx, q, orgID, StatusOrgSelected)
	if err != nil {
		return nil, fmt.Errorf("openid4vppresenter: list pending org %s: %w", orgID, err)
	}
	defer rows.Close()

	out := []Transaction{}
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("openid4vppresenter: list pending scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("openid4vppresenter: list pending rows: %w", err)
	}
	return out, nil
}

// Complete consumes an org_selected transaction as completed: the Authorization
// Response has been delivered.
func (s *Store) Complete(ctx context.Context, id uuid.UUID) error {
	return s.consume(ctx, id, StatusCompleted, audit.PresentationCompleted, nil)
}

// Deny consumes an org_selected transaction as denied, recording why (a short,
// code-like reason — never response material).
func (s *Store) Deny(ctx context.Context, id uuid.UUID, reason string) error {
	return s.consume(ctx, id, StatusDenied, audit.PresentationDenied, map[string]any{"reason": reason})
}

func (s *Store) consume(ctx context.Context, id uuid.UUID, status, action string, extra map[string]any) error {
	const q = `
		UPDATE openid4vp_transactions
		SET status = $2, consumed_at = now()
		WHERE id = $1 AND status = $3 AND consumed_at IS NULL
		RETURNING ` + transactionColumns
	return database.InTx(ctx, s.db, func(tx database.Querier) error {
		t, err := scanTransaction(tx.QueryRow(ctx, q, id, status, StatusOrgSelected))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotPending
		}
		if err != nil {
			return fmt.Errorf("openid4vppresenter: consume as %s: %w", status, err)
		}
		meta := map[string]any{"verifier": t.VerifierIdentity}
		for k, v := range extra {
			meta[k] = v
		}
		return s.audit.Record(ctx, tx, action,
			audit.Target{Type: audit.TargetPresentationTransaction, ID: t.ID.String(), OrgID: t.OrganizationID},
			audit.Created(meta))
	})
}

// Prune marks every unconsumed row past its TTL as expired (auditing each, so
// an abandoned request is visible in the org's log once an org was selected) and
// deletes rows consumed longer than consumedRetention ago. It returns how many
// rows it touched; the caller runs it on the shared prune interval.
func (s *Store) Prune(ctx context.Context) (int64, error) {
	var touched int64
	err := database.InTx(ctx, s.db, func(tx database.Querier) error {
		const expire = `
			UPDATE openid4vp_transactions
			SET status = $1, consumed_at = now()
			WHERE consumed_at IS NULL AND expires_at < now()
			RETURNING id, organization_id, verifier_identity`
		rows, err := tx.Query(ctx, expire, StatusExpired)
		if err != nil {
			return fmt.Errorf("openid4vppresenter: expire: %w", err)
		}
		type expired struct {
			id       uuid.UUID
			orgID    *uuid.UUID
			verifier string
		}
		var expiredRows []expired
		for rows.Next() {
			var e expired
			if err := rows.Scan(&e.id, &e.orgID, &e.verifier); err != nil {
				rows.Close()
				return fmt.Errorf("openid4vppresenter: expire scan: %w", err)
			}
			expiredRows = append(expiredRows, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("openid4vppresenter: expire rows: %w", err)
		}
		for _, e := range expiredRows {
			if err := s.audit.Record(ctx, tx, audit.PresentationExpired,
				audit.Target{Type: audit.TargetPresentationTransaction, ID: e.id.String(), OrgID: e.orgID},
				audit.Created(map[string]any{"verifier": e.verifier})); err != nil {
				return err
			}
		}
		touched += int64(len(expiredRows))

		const remove = `DELETE FROM openid4vp_transactions WHERE consumed_at < now() - $1::interval`
		tag, err := tx.Exec(ctx, remove, consumedRetention)
		if err != nil {
			return fmt.Errorf("openid4vppresenter: delete consumed: %w", err)
		}
		touched += tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return touched, nil
}
