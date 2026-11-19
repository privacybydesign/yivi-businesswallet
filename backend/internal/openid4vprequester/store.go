package openid4vprequester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// listLimit caps the sent-requests list; the console shows the recent ones.
const listLimit = 100

// Store persists openid4vp_outbound_requests and writes each state change's
// audit event in the same transaction.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

const requestColumns = `
	id, organization_id, created_by, sender_address, recipient_address, qerds_message_id,
	client_id, nonce, state, credentials, request_object, request_fetched_at, status,
	failure_reason, disclosed, created_at, expires_at, responded_at`

func scanRequest(row pgx.Row) (Request, error) {
	var (
		r           Request
		credentials []byte
		disclosed   []byte
	)
	err := row.Scan(
		&r.ID, &r.OrganizationID, &r.CreatedBy, &r.SenderAddress, &r.RecipientAddress, &r.QerdsMessageID,
		&r.ClientID, &r.Nonce, &r.State, &credentials, &r.RequestObject, &r.RequestFetchedAt, &r.Status,
		&r.FailureReason, &disclosed, &r.CreatedAt, &r.ExpiresAt, &r.RespondedAt,
	)
	if err != nil {
		return Request{}, err
	}
	if err := json.Unmarshal(credentials, &r.Credentials); err != nil {
		return Request{}, fmt.Errorf("openid4vprequester: decode credentials: %w", err)
	}
	if len(disclosed) > 0 {
		if err := json.Unmarshal(disclosed, &r.Disclosed); err != nil {
			return Request{}, fmt.Errorf("openid4vprequester: decode disclosed: %w", err)
		}
	}
	return r, nil
}

// vcts lists the credential types asked for — what the audit log names instead
// of the query itself.
func vcts(creds []CredentialRequest) []string {
	out := make([]string, 0, len(creds))
	for _, c := range creds {
		out = append(out, c.VCT)
	}
	return out
}

// NewRequest is what Create persists.
type NewRequest struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	CreatedBy        *uuid.UUID
	SenderAddress    string
	RecipientAddress string
	ClientID         string
	Nonce            string
	State            string
	Credentials      []CredentialRequest
	RequestObject    string
	ExpiresAt        time.Time
}

// Create stores a signed request at StatusSent and audits it.
func (s *Store) Create(ctx context.Context, in NewRequest) (Request, error) {
	credentials, err := json.Marshal(in.Credentials)
	if err != nil {
		return Request{}, fmt.Errorf("openid4vprequester: encode credentials: %w", err)
	}
	const q = `
		INSERT INTO openid4vp_outbound_requests (
			id, organization_id, created_by, sender_address, recipient_address, client_id,
			nonce, state, credentials, request_object, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING ` + requestColumns
	var r Request
	err = database.InTx(ctx, s.db, func(tx database.Querier) error {
		r, err = scanRequest(tx.QueryRow(ctx, q,
			in.ID, in.OrganizationID, in.CreatedBy, in.SenderAddress, in.RecipientAddress, in.ClientID,
			in.Nonce, in.State, credentials, in.RequestObject, StatusSent, in.ExpiresAt))
		if err != nil {
			return fmt.Errorf("openid4vprequester: create: %w", err)
		}
		return s.audit.Record(ctx, tx, audit.PresentationRequestSent,
			audit.Target{Type: audit.TargetOutboundPresentationRequest, ID: r.ID.String(), OrgID: &r.OrganizationID},
			audit.Created(map[string]any{
				"recipient":   r.RecipientAddress,
				"sender":      r.SenderAddress,
				"credentials": vcts(r.Credentials),
			}))
	})
	if err != nil {
		return Request{}, err
	}
	return r, nil
}

// SetMessage links the QERDS message that carried the invocation.
func (s *Store) SetMessage(ctx context.Context, id, messageID uuid.UUID) error {
	const q = `UPDATE openid4vp_outbound_requests SET qerds_message_id = $2 WHERE id = $1`
	if _, err := s.db.Exec(ctx, q, id, messageID); err != nil {
		return fmt.Errorf("openid4vprequester: set message: %w", err)
	}
	return nil
}

// Get loads a request by id, whatever its organization — for the public
// request_uri/response_uri endpoints, which have no tenant.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Request, error) {
	r, err := scanRequest(s.db.QueryRow(ctx, `SELECT `+requestColumns+` FROM openid4vp_outbound_requests WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("openid4vprequester: get: %w", err)
	}
	return r, nil
}

// GetForOrg loads a request only if it belongs to orgID.
func (s *Store) GetForOrg(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	r, err := scanRequest(s.db.QueryRow(ctx,
		`SELECT `+requestColumns+` FROM openid4vp_outbound_requests WHERE id = $1 AND organization_id = $2`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("openid4vprequester: get for org: %w", err)
	}
	return r, nil
}

// ListForOrg returns the organization's most recent requests, newest first.
func (s *Store) ListForOrg(ctx context.Context, orgID uuid.UUID) ([]Request, error) {
	rows, err := s.db.Query(ctx, `SELECT `+requestColumns+` FROM openid4vp_outbound_requests
		WHERE organization_id = $1 ORDER BY created_at DESC LIMIT $2`, orgID, listLimit)
	if err != nil {
		return nil, fmt.Errorf("openid4vprequester: list: %w", err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("openid4vprequester: list: %w", err)
	}
	return out, nil
}

// FetchRequestObject hands out the signed Request Object exactly once, while the
// request is open: the fetch and its mark are one UPDATE, so two concurrent
// fetches cannot both get it. Anything else — unknown, fetched, answered or
// expired — is ErrNotFound, telling a caller nothing about which.
func (s *Store) FetchRequestObject(ctx context.Context, id uuid.UUID) (string, error) {
	const q = `
		UPDATE openid4vp_outbound_requests SET request_fetched_at = now()
		WHERE id = $1 AND request_fetched_at IS NULL AND status = $2 AND expires_at > now()
		RETURNING request_object`
	var jar string
	err := s.db.QueryRow(ctx, q, id, StatusSent).Scan(&jar)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("openid4vprequester: fetch request object: %w", err)
	}
	return jar, nil
}

// Complete records the verified answer. Scoped to a still-open sent request, so
// a second answer — or one racing it — finds nothing (ErrNotPending).
func (s *Store) Complete(ctx context.Context, id uuid.UUID, disclosed []DisclosedCredential) error {
	body, err := json.Marshal(disclosed)
	if err != nil {
		return fmt.Errorf("openid4vprequester: encode disclosed: %w", err)
	}
	presented := make([]string, 0, len(disclosed))
	for _, d := range disclosed {
		presented = append(presented, d.VCT)
	}
	return s.settle(ctx, id, StatusCompleted, nil, body, audit.PresentationResponseReceived,
		map[string]any{"status": StatusCompleted, "credentials": presented})
}

// Fail consumes an open request whose answer could not be accepted, or whose
// invocation could not be delivered.
func (s *Store) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	return s.settle(ctx, id, StatusFailed, &reason, nil, audit.PresentationRequestFailed,
		map[string]any{"status": StatusFailed, "reason": reason})
}

// settle moves an open request to its terminal status and records action.
func (s *Store) settle(ctx context.Context, id uuid.UUID, status string, reason *string, disclosed []byte, action string, after map[string]any) error {
	const q = `
		UPDATE openid4vp_outbound_requests
		SET status = $2, failure_reason = $3, disclosed = $4, responded_at = now()
		WHERE id = $1 AND status = $5 AND expires_at > now()
		RETURNING organization_id`
	return database.InTx(ctx, s.db, func(tx database.Querier) error {
		var orgID uuid.UUID
		err := tx.QueryRow(ctx, q, id, status, reason, disclosed, StatusSent).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotPending
		}
		if err != nil {
			return fmt.Errorf("openid4vprequester: settle %s: %w", status, err)
		}
		return s.audit.Record(ctx, tx, action,
			audit.Target{Type: audit.TargetOutboundPresentationRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": StatusSent}, after))
	})
}
