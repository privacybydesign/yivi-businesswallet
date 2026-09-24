package proofing

import (
	"context"
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

// requestColumns is the SELECT list scanRequest reads, over requestFrom.
const requestColumns = `r.id, r.organization_id, r.requested_by,
	COALESCE(NULLIF(TRIM(COALESCE(u.given_names, '') || ' ' || COALESCE(u.last_name, '')), ''), u.email, ''),
	r.subject_name, r.subject_email, r.flow_id, r.flow_name, r.status, r.link_expires_at,
	COALESCE(r.assurance_level, ''), COALESCE(r.eidas_level, ''), COALESCE(r.error_code, ''),
	r.created_at, r.updated_at, r.completed_at,
	r.ips_session_id, r.ips_session_token_ciphertext, r.ips_session_expires_at, r.ips_session_ended_at,
	r.subject_user_id, COALESCE(r.flow_version, 0),
	r.customer_id, COALESCE(c.name, ''), r.proofed_name_ciphertext`

// requestFrom joins a request to its sender and its customer.
const requestFrom = ` FROM identity_proofing_requests r
	LEFT JOIN users u ON u.id = r.requested_by
	LEFT JOIN identity_proofing_customers c ON c.id = r.customer_id`

func (s *RequestStore) scanRequest(row pgx.Row) (Request, error) {
	var r Request
	var sessionID *string
	var tokenCT []byte
	var sessionExpiresAt, sessionEndedAt *time.Time
	var nameCT []byte
	if err := row.Scan(&r.ID, &r.OrganizationID, &r.RequestedBy, &r.RequestedByName,
		&r.SubjectName, &r.SubjectEmail, &r.FlowID, &r.FlowName, &r.Status, &r.LinkExpiresAt,
		&r.AssuranceLevel, &r.EIDASLevel, &r.ErrorCode, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt,
		&sessionID, &tokenCT, &sessionExpiresAt, &sessionEndedAt, &r.SubjectUserID, &r.FlowVersion,
		&r.CustomerID, &r.CustomerName, &nameCT); err != nil {
		return Request{}, err
	}
	if nameCT != nil {
		if s.cipher == nil {
			return Request{}, ErrNoEncryptionKey
		}
		name, err := s.cipher.Decrypt(nameCT)
		if err != nil {
			return Request{}, fmt.Errorf("proofing: decrypt proofed name request %s: %w", r.ID, err)
		}
		r.ProofedName = string(name)
	}
	if sessionID != nil {
		if s.cipher == nil {
			return Request{}, ErrNoEncryptionKey
		}
		token, err := s.cipher.Decrypt(tokenCT)
		if err != nil {
			return Request{}, fmt.Errorf("proofing: decrypt session token request %s: %w", r.ID, err)
		}
		r.session = &ipsSession{ID: *sessionID, Token: string(token), EndedAt: sessionEndedAt}
		if sessionExpiresAt != nil {
			r.session.ExpiresAt = *sessionExpiresAt
		}
	}
	return r, nil
}

// NewStoredRequest is a request about to be mailed. It has no IPS session yet:
// that is created when the recipient starts (AttachSession).
type NewStoredRequest struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	RequestedBy   uuid.UUID
	Subject       Subject
	Flow          proofingprovider.Flow
	LinkExpiresAt time.Time
}

// Create stores a new request and audits identity_proofing.requested in the same
// transaction.
func (s *RequestStore) Create(ctx context.Context, in NewStoredRequest) (Request, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO identity_proofing_requests
			(id, organization_id, requested_by, subject_user_id, customer_id, subject_name, subject_email,
			 flow_id, flow_name, flow_version, link_expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
		if _, err := q.Exec(ctx, insert, in.ID, in.OrgID, in.RequestedBy, in.Subject.UserID, in.Subject.CustomerID,
			in.Subject.Name, in.Subject.Email, in.Flow.ID, in.Flow.Name, in.Flow.Version, in.LinkExpiresAt); err != nil {
			return fmt.Errorf("proofing: create request org %s: %w", in.OrgID, err)
		}
		fields := withAuditSubject(map[string]any{
			"flowId": in.Flow.ID, "flowName": in.Flow.Name, "flowVersion": in.Flow.Version,
		}, in.Subject.Name, in.Subject.Email)
		if in.Subject.UserID != nil {
			fields["subjectUserId"] = in.Subject.UserID.String()
		}
		if in.Subject.CustomerID != nil {
			fields["customerId"] = in.Subject.CustomerID.String()
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingRequested,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: in.ID.String(), OrgID: &in.OrgID},
			audit.Created(fields))
	})
	if err != nil {
		return Request{}, err
	}
	return s.get(ctx, in.ID)
}

func (s *RequestStore) get(ctx context.Context, id uuid.UUID) (Request, error) {
	query := `SELECT ` + requestColumns + requestFrom + ` WHERE r.id = $1`
	req, err := s.scanRequest(s.db.QueryRow(ctx, query, id))
	if err != nil {
		return Request{}, fmt.Errorf("proofing: get request %s: %w", id, err)
	}
	return req, nil
}

// List returns the org's requests, newest first, narrowed by filter.
func (s *RequestStore) List(ctx context.Context, orgID uuid.UUID, filter RequestFilter) ([]Request, error) {
	query := `SELECT ` + requestColumns + requestFrom + `
		WHERE r.organization_id = $1 AND ($2::uuid IS NULL OR r.requested_by = $2)
			AND ($3::uuid IS NULL OR r.customer_id = $3)
		ORDER BY r.created_at DESC LIMIT $4`
	rows, err := s.db.Query(ctx, query, orgID, filter.RequestedBy, filter.CustomerID, maxListedRequests)
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

// AttachSession stores the IPS session a request was sent with, and audits
// identity_proofing.session_created in the same transaction; the flow version
// becomes the one the session pinned. It reports false, storing nothing, when
// the row already has a session or moved on.
func (s *RequestStore) AttachSession(ctx context.Context, req Request, sess proofingprovider.Session) (bool, error) {
	if s.cipher == nil {
		return false, ErrNoEncryptionKey
	}
	tokenCT, err := s.cipher.Encrypt([]byte(sess.Token))
	if err != nil {
		return false, fmt.Errorf("proofing: encrypt session token request %s: %w", req.ID, err)
	}
	attached := false
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET
				ips_session_id = $2, ips_session_token_ciphertext = $3, ips_session_expires_at = $4,
				flow_version = COALESCE(NULLIF($5, 0), flow_version), updated_at = now()
			WHERE id = $1 AND ips_session_id IS NULL AND status = 'pending'`
		tag, err := q.Exec(ctx, update, req.ID, sess.ID, tokenCT, sess.ExpiresAt, sess.FlowVersion)
		if err != nil {
			return fmt.Errorf("proofing: attach session request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		attached = true
		return s.audit.Record(ctx, q, audit.IdentityProofingSessionCreated,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{
				"flowId": req.FlowID, "flowVersion": sess.FlowVersion, "sessionExpiresAt": sess.ExpiresAt,
			})))
	})
	return attached, err
}

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
			audit.Updated(
				req.auditFields(map[string]any{"status": string(req.Status)}),
				req.auditFields(map[string]any{"status": string(StatusInProgress)})))
	})
}

// EndSession records that the request's IPS session ended without an outcome
// (expired, or cancelled at IPS), which ends the request: it reads as expired
// from then on. Audited identity_proofing.session_ended with IPS's status. A
// no-op once recorded, so concurrent pollers record it once.
func (s *RequestStore) EndSession(ctx context.Context, req Request, sessionID string, ipsStatus proofingprovider.Status) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET ips_session_ended_at = now(), updated_at = now()
			WHERE id = $1 AND ips_session_id = $2 AND ips_session_ended_at IS NULL
				AND status IN ('pending', 'in_progress')`
		tag, err := q.Exec(ctx, update, req.ID, sessionID)
		if err != nil {
			return fmt.Errorf("proofing: end session request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingSessionEnded,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(
				req.auditFields(map[string]any{"status": string(req.Status)}),
				req.auditFields(map[string]any{"status": string(StatusExpired), "ipsStatus": string(ipsStatus)})))
	})
}

// outcomeActions is the audit action each IPS outcome is recorded under.
var outcomeActions = map[Status]string{
	StatusApproved:    audit.IdentityProofingApproved,
	StatusRejected:    audit.IdentityProofingRejected,
	StatusNeedsReview: audit.IdentityProofingNeedsReview,
}

// auditFields adds whom the request is about to an audit snapshot, so every
// event on it names its subject. Both sides of an update carry it unchanged.
func (r Request) auditFields(fields map[string]any) map[string]any {
	return withAuditSubject(fields, r.SubjectName, r.SubjectEmail)
}

// withAuditSubject adds a subject's e-mail address, and name when sent with
// one, to an audit snapshot. Never the proofed name: that is not audited.
func withAuditSubject(fields map[string]any, name, email string) map[string]any {
	if name != "" {
		fields["subjectName"] = name
	}
	fields["subjectEmail"] = email
	return fields
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
// it as identity_proofing.approved, .rejected or .needs_review, with IPS's error
// code as the reason, in the same transaction. A non-empty res.Name is
// sealed and kept until ProofedNameRetention has passed; it is never audited. It
// is a no-op when the row already holds that outcome or moved to another
// session, so concurrent pollers record a decision once.
func (s *RequestStore) RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error {
	action, ok := outcomeActions[status]
	if !ok {
		return fmt.Errorf("proofing: record outcome request %s: %q is not an outcome", req.ID, status)
	}
	var nameCT []byte
	var purgeAfter *time.Time
	if res.Name != "" {
		if s.cipher == nil {
			return ErrNoEncryptionKey
		}
		var err error
		if nameCT, err = s.cipher.Encrypt([]byte(res.Name)); err != nil {
			return fmt.Errorf("proofing: encrypt proofed name request %s: %w", req.ID, err)
		}
		at := time.Now().Add(ProofedNameRetention)
		purgeAfter = &at
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET
				status = $3, assurance_level = NULLIF($4, ''), eidas_level = NULLIF($5, ''),
				error_code = NULLIF($6, ''), completed_at = COALESCE($7, now()), updated_at = now(),
				proofed_name_ciphertext = $8, proofed_name_purge_after = $9
			WHERE id = $1 AND ips_session_id = $2
				AND status IN ('pending', 'in_progress', 'needs_review') AND status <> $3`
		tag, err := q.Exec(ctx, update, req.ID, sessionID, string(status),
			res.AssuranceLevel, res.EIDASLevel, res.ErrorCode, res.CompletedAt, nameCT, purgeAfter)
		if err != nil {
			return fmt.Errorf("proofing: record outcome request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		after := map[string]any{"status": string(status)}
		for key, value := range map[string]string{
			"assuranceLevel": res.AssuranceLevel, "eidasLevel": res.EIDASLevel, "errorCode": res.ErrorCode,
		} {
			if value != "" {
				after[key] = value
			}
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(req.auditFields(map[string]any{"status": string(req.Status)}), req.auditFields(after)))
	})
}

// PurgeProofedNames clears every proofed name past its retention, for the
// pruner. The request and its outcome stay. Not audited: nothing was decided.
func (s *RequestStore) PurgeProofedNames(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `UPDATE identity_proofing_requests
		SET proofed_name_ciphertext = NULL, proofed_name_purge_after = NULL, updated_at = now()
		WHERE proofed_name_purge_after <= now()`)
	if err != nil {
		return 0, fmt.Errorf("proofing: purge proofed names: %w", err)
	}
	return tag.RowsAffected(), nil
}
