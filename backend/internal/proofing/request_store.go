package proofing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const linkTokenBytes = 32

func newLinkToken() (string, [sha256.Size]byte, error) {
	b := make([]byte, linkTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("proofing: link token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

// RequestStore persists proofing requests. The IPS session bearer token is sealed
// with the same deployment key as the org's API key.
type RequestStore struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewRequestStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *RequestStore {
	return &RequestStore{db: db, audit: recorder, cipher: cipher}
}

// requestColumns is the SELECT list scanRequest reads, over identity_proofing_requests r
// LEFT JOIN users u ON u.id = r.requested_by.
const requestColumns = `r.id, r.organization_id, r.requested_by,
	COALESCE(NULLIF(TRIM(COALESCE(u.given_names, '') || ' ' || COALESCE(u.last_name, '')), ''), u.email, ''),
	r.subject_name, r.subject_email, r.flow_id, r.flow_name, r.status, r.link_expires_at,
	COALESCE(r.assurance_level, ''), COALESCE(r.eidas_level, ''), COALESCE(r.error_code, ''),
	r.created_at, r.updated_at, r.completed_at,
	r.ips_session_id, r.ips_session_token_ciphertext, r.ips_session_expires_at,
	r.subject_user_id, COALESCE(r.flow_version, 0)`

func (s *RequestStore) scanRequest(row pgx.Row) (Request, error) {
	var r Request
	var sessionID *string
	var tokenCT []byte
	var sessionExpiresAt *time.Time
	if err := row.Scan(&r.ID, &r.OrganizationID, &r.RequestedBy, &r.RequestedByName,
		&r.SubjectName, &r.SubjectEmail, &r.FlowID, &r.FlowName, &r.Status, &r.LinkExpiresAt,
		&r.AssuranceLevel, &r.EIDASLevel, &r.ErrorCode, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt,
		&sessionID, &tokenCT, &sessionExpiresAt, &r.SubjectUserID, &r.FlowVersion); err != nil {
		return Request{}, err
	}
	if sessionID != nil {
		if s.cipher == nil {
			return Request{}, ErrNoEncryptionKey
		}
		token, err := s.cipher.Decrypt(tokenCT)
		if err != nil {
			return Request{}, fmt.Errorf("proofing: decrypt session token request %s: %w", r.ID, err)
		}
		r.session = &ipsSession{ID: *sessionID, Token: string(token)}
		if sessionExpiresAt != nil {
			r.session.ExpiresAt = *sessionExpiresAt
		}
	}
	return r, nil
}

// NewStoredRequest is a request whose IPS session was just created: the row is
// written with the session attached, and the link expires with the session.
type NewStoredRequest struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	RequestedBy   uuid.UUID
	Subject       Member
	Flow          proofingprovider.Flow
	Session       proofingprovider.Session
	LinkExpiresAt time.Time
}

// Create stores a new request and audits identity_proofing.requested in the same
// transaction. It returns the raw link token, which only the e-mail carries.
func (s *RequestStore) Create(ctx context.Context, in NewStoredRequest) (Request, string, error) {
	if s.cipher == nil {
		return Request{}, "", ErrNoEncryptionKey
	}
	raw, hash, err := newLinkToken()
	if err != nil {
		return Request{}, "", err
	}
	tokenCT, err := s.cipher.Encrypt([]byte(in.Session.Token))
	if err != nil {
		return Request{}, "", fmt.Errorf("proofing: encrypt session token request %s: %w", in.ID, err)
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO identity_proofing_requests
			(id, organization_id, requested_by, subject_user_id, subject_name, subject_email,
			 flow_id, flow_name, flow_version, token_hash, link_expires_at,
			 ips_session_id, ips_session_token_ciphertext, ips_session_expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
		if _, err := q.Exec(ctx, insert, in.ID, in.OrgID, in.RequestedBy, in.Subject.UserID, in.Subject.Name,
			in.Subject.Email, in.Flow.ID, in.Flow.Name, in.Flow.Version, hash[:], in.LinkExpiresAt,
			in.Session.ID, tokenCT, in.Session.ExpiresAt); err != nil {
			return fmt.Errorf("proofing: create request org %s: %w", in.OrgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingRequested,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: in.ID.String(), OrgID: &in.OrgID},
			audit.Created(map[string]any{
				"subjectUserId": in.Subject.UserID.String(), "subjectName": in.Subject.Name,
				"subjectEmail": in.Subject.Email, "flowId": in.Flow.ID, "flowName": in.Flow.Name,
				"flowVersion": in.Flow.Version,
			}))
	})
	if err != nil {
		return Request{}, "", err
	}
	req, err := s.get(ctx, in.ID)
	return req, raw, err
}

func (s *RequestStore) get(ctx context.Context, id uuid.UUID) (Request, error) {
	query := `SELECT ` + requestColumns + `
		FROM identity_proofing_requests r LEFT JOIN users u ON u.id = r.requested_by
		WHERE r.id = $1`
	req, err := s.scanRequest(s.db.QueryRow(ctx, query, id))
	if err != nil {
		return Request{}, fmt.Errorf("proofing: get request %s: %w", id, err)
	}
	return req, nil
}

// List returns the org's requests, newest first. A non-nil requestedBy narrows
// it to the requests that user sent.
func (s *RequestStore) List(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]Request, error) {
	query := `SELECT ` + requestColumns + `
		FROM identity_proofing_requests r LEFT JOIN users u ON u.id = r.requested_by
		WHERE r.organization_id = $1 AND ($2::uuid IS NULL OR r.requested_by = $2)
		ORDER BY r.created_at DESC LIMIT $3`
	rows, err := s.db.Query(ctx, query, orgID, requestedBy, maxListedRequests)
	if err != nil {
		return nil, fmt.Errorf("proofing: list requests org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		req, err := s.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan request org %s: %w", orgID, err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: list requests org %s: %w", orgID, err)
	}
	return out, nil
}

// ByToken resolves a raw link token, or ErrLinkNotFound for an unknown or
// expired link.
func (s *RequestStore) ByToken(ctx context.Context, rawToken string) (Link, error) {
	hash := sha256.Sum256([]byte(rawToken))
	query := `SELECT ` + requestColumns + `, o.name
		FROM identity_proofing_requests r
		LEFT JOIN users u ON u.id = r.requested_by
		JOIN organizations o ON o.id = r.organization_id
		WHERE r.token_hash = $1 AND r.link_expires_at > now()`
	var link Link
	row := s.db.QueryRow(ctx, query, hash[:])
	req, err := s.scanRequest(rowWithTail{row: row, tail: []any{&link.OrganizationName}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrLinkNotFound
	}
	if err != nil {
		return Link{}, fmt.Errorf("proofing: request by token: %w", err)
	}
	link.Request = req
	return link, nil
}

// rowWithTail scans extra trailing columns after scanRequest's own.
type rowWithTail struct {
	row  pgx.Row
	tail []any
}

func (r rowWithTail) Scan(dest ...any) error { return r.row.Scan(append(dest, r.tail...)...) }

// MarkStarted records that the subject's phone joined the session (IPS reports
// it opened), and audits identity_proofing.session_started. A no-op when the row
// already moved on, so concurrent pollers record it once.
func (s *RequestStore) MarkStarted(ctx context.Context, req Request, sessionID string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET status = 'in_progress', updated_at = now()
			WHERE id = $1 AND ips_session_id = $2 AND status = 'pending'`
		tag, err := q.Exec(ctx, update, req.ID, sessionID)
		if err != nil {
			return fmt.Errorf("proofing: mark started request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingSessionStarted,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(map[string]any{"status": string(req.Status)}, map[string]any{"status": string(StatusInProgress)}))
	})
}

// ExpireLink ends a request's link now, because its IPS session ended (expired
// or cancelled at IPS) without an outcome: the link and the session share one
// lifetime. Not audited: nothing was decided.
func (s *RequestStore) ExpireLink(ctx context.Context, id uuid.UUID, sessionID string) error {
	const update = `UPDATE identity_proofing_requests SET
			link_expires_at = LEAST(link_expires_at, now()), updated_at = now()
		WHERE id = $1 AND ips_session_id = $2 AND status IN ('pending', 'in_progress')`
	if _, err := s.db.Exec(ctx, update, id, sessionID); err != nil {
		return fmt.Errorf("proofing: expire link request %s: %w", id, err)
	}
	return nil
}

// Members lists the org's members (admins, members, employees and externals
// alike), ordered by name: the people a request can be sent to.
func (s *RequestStore) Members(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	rows, err := s.db.Query(ctx, `SELECT `+memberColumns+`
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		ORDER BY lower(`+memberNameExpr+`), u.email`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list members org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan member org %s: %w", orgID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: list members org %s: %w", orgID, err)
	}
	return out, nil
}

// Member returns one member of the org, or ErrMemberNotFound.
func (s *RequestStore) Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error) {
	m, err := scanMember(s.db.QueryRow(ctx, `SELECT `+memberColumns+`
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.user_id = $2`, orgID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrMemberNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("proofing: read member %s org %s: %w", userID, orgID, err)
	}
	return m, nil
}

// memberNameExpr is a member's display name: preferred name, else given names
// plus last name, else the e-mail address.
const memberNameExpr = `COALESCE(NULLIF(TRIM(COALESCE(u.preferred_name, '')), ''),
	NULLIF(TRIM(COALESCE(u.given_names, '') || ' ' || COALESCE(u.last_name, '')), ''), u.email)`

const memberColumns = `u.id, ` + memberNameExpr + `, u.email, m.role, m.member_type, COALESCE(m.external_organisation, '')`

func scanMember(row pgx.Row) (Member, error) {
	var m Member
	err := row.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.MemberType, &m.ExternalOrganisation)
	return m, err
}

// RecordOutcome stores an IPS outcome for the session the caller read, and audits
// identity_proofing.completed in the same transaction. It is a no-op when the row
// already holds that outcome or moved to another session, so concurrent pollers
// record a decision once.
func (s *RequestStore) RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET
				status = $3, assurance_level = NULLIF($4, ''), eidas_level = NULLIF($5, ''),
				error_code = NULLIF($6, ''), completed_at = COALESCE($7, now()), updated_at = now()
			WHERE id = $1 AND ips_session_id = $2
				AND status IN ('pending', 'in_progress', 'needs_review') AND status <> $3`
		tag, err := q.Exec(ctx, update, req.ID, sessionID, string(status),
			res.AssuranceLevel, res.EIDASLevel, res.ErrorCode, res.CompletedAt)
		if err != nil {
			return fmt.Errorf("proofing: record outcome request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCompleted,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(
				map[string]any{"status": string(req.Status)},
				map[string]any{
					"status": string(status), "assuranceLevel": res.AssuranceLevel,
					"eidasLevel": res.EIDASLevel, "errorCode": res.ErrorCode,
				}))
	})
}
