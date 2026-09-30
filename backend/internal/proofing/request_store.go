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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
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
	r.customer_id, COALESCE(c.name, ''), r.proofed_name_ciphertext, c.data_retention_days,
	r.api_key_id, COALESCE(k.name, ''), COALESCE(r.method, ''), COALESCE(r.yivi_transaction_id, ''),
	COALESCE(r.required_assurance_level, ''), r.mode, r.link_token_hash IS NOT NULL,
	r.cancelled_at, r.purged_at, COALESCE(r.redirect_url, ''), COALESCE(r.language, ''),
	` + purgeAtExpr

// purgeAtExpr is when a customer's request is purged: its customer's retention
// after it settled. NULL while it runs or awaits review, and for a member's.
const purgeAtExpr = `(COALESCE(r.cancelled_at, r.ips_session_ended_at,
		CASE WHEN r.status IN ('approved', 'rejected') THEN r.completed_at END,
		CASE WHEN r.status IN ('pending', 'in_progress') AND r.ips_session_expires_at <= now()
			THEN r.ips_session_expires_at END)
	+ make_interval(days => c.data_retention_days))`

// requestFrom joins a request to its sender and its customer.
const requestFrom = ` FROM identity_proofing_requests r
	LEFT JOIN users u ON u.id = r.requested_by
	LEFT JOIN identity_proofing_customers c ON c.id = r.customer_id
	LEFT JOIN identity_proofing_api_keys k ON k.id = r.api_key_id`

func (s *RequestStore) scanRequest(row pgx.Row) (Request, error) {
	var r Request
	var sessionID *string
	var tokenCT []byte
	var sessionExpiresAt, sessionEndedAt *time.Time
	var nameCT []byte
	var retentionDays *int
	if err := row.Scan(&r.ID, &r.OrganizationID, &r.RequestedBy, &r.RequestedByName,
		&r.SubjectName, &r.SubjectEmail, &r.FlowID, &r.FlowName, &r.Status, &r.LinkExpiresAt,
		&r.AssuranceLevel, &r.EIDASLevel, &r.ErrorCode, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt,
		&sessionID, &tokenCT, &sessionExpiresAt, &sessionEndedAt, &r.SubjectUserID, &r.FlowVersion,
		&r.CustomerID, &r.CustomerName, &nameCT, &retentionDays, &r.APIKeyID, &r.APIKeyName, &r.Method, &r.yiviTransactionID,
		&r.RequiredAssuranceLevel, &r.Mode, &r.Hosted, &r.CancelledAt, &r.PurgedAt, &r.RedirectURL, &r.Language,
		&r.PurgeAt); err != nil {
		return Request{}, err
	}
	r.NameRetention = ProofedNameRetention
	if retentionDays != nil {
		r.NameRetention = CustomerSettings{DataRetentionDays: *retentionDays}.DataRetention()
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

// NewStoredRequest is a request about to be stored. Its IPS session is already
// created and is attached right after (AttachSession).
type NewStoredRequest struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// RequestedBy is the member who sent it, APIKeyID the customer key that
	// created it; exactly one is set.
	RequestedBy   *uuid.UUID
	APIKeyID      *uuid.UUID
	Subject       Subject
	Flow          proofingprovider.Flow
	LinkExpiresAt time.Time
	// Method is the app the session was created for; IPS's later report of
	// the app the subject used replaces it.
	Method  proofingprovider.Method
	Channel Channel
	// Mode is ModeTest for a sandbox request; empty is ModeLive.
	Mode Mode
	// LinkTokenHash is set for a hosted request: the SHA-256 of its link's token.
	LinkTokenHash []byte
	// RedirectURL and Language are a hosted request's; empty for none.
	RedirectURL string
	Language    email.Locale
}

// Create stores a new request and audits identity_proofing.requested in the same
// transaction.
func (s *RequestStore) Create(ctx context.Context, in NewStoredRequest) (Request, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO identity_proofing_requests
			(id, organization_id, requested_by, subject_user_id, customer_id, subject_name, subject_email,
			 flow_id, flow_name, flow_version, link_expires_at, api_key_id, method, required_assurance_level, mode,
			 link_token_hash, redirect_url, language)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), NULLIF($14, ''),
				COALESCE(NULLIF($15, ''), 'live'), $16, NULLIF($17, ''), NULLIF($18, ''))`
		if _, err := q.Exec(ctx, insert, in.ID, in.OrgID, in.RequestedBy, in.Subject.UserID, in.Subject.CustomerID,
			in.Subject.Name, in.Subject.Email, in.Flow.ID, in.Flow.Name, in.Flow.Version, in.LinkExpiresAt,
			in.APIKeyID, string(in.Method), in.Flow.RequiredAssuranceLevel, string(in.Mode), in.LinkTokenHash,
			in.RedirectURL, string(in.Language)); err != nil {
			return fmt.Errorf("proofing: create request org %s: %w", in.OrgID, err)
		}
		fields := withAuditSubject(map[string]any{
			"flowId": in.Flow.ID, "flowName": in.Flow.Name, "flowVersion": in.Flow.Version,
			"channel": string(in.Channel),
		}, in.Subject.Name, in.Subject.Email)
		fields = withMethod(fields, in.Method)
		if in.Subject.UserID != nil {
			fields["subjectUserId"] = in.Subject.UserID.String()
		}
		if in.Subject.CustomerID != nil {
			fields["customerId"] = in.Subject.CustomerID.String()
		}
		if in.APIKeyID != nil {
			fields["apiKeyId"] = in.APIKeyID.String()
		}
		// A hosted link lapses unstarted at link_expires_at: wake the deadline job.
		if in.LinkTokenHash != nil {
			if err := database.Notify(ctx, q, SessionChannel); err != nil {
				return err
			}
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

// liveSessionWhere is a request whose IPS session is attached, not seen to
// end, and still waiting on the subject (a review has no deadline).
const liveSessionWhere = `r.ips_session_id IS NOT NULL AND r.ips_session_ended_at IS NULL
	AND r.status IN ('pending', 'in_progress')`

// ListDue leases and returns up to limit requests, across every org, whose
// session's cap has passed without the wallet seeing it end: the deadline job's
// work list. A leased request is not offered again for deadlineRetry, so
// replicas running the job never re-check the same session at once.
func (s *RequestStore) ListDue(ctx context.Context, now time.Time, limit int) ([]Request, error) {
	query := `WITH due AS (
			SELECT r.id FROM identity_proofing_requests r
			WHERE ` + liveSessionWhere + ` AND r.ips_session_expires_at <= $1
				AND (r.ips_reconcile_leased_until IS NULL OR r.ips_reconcile_leased_until <= $1)
			ORDER BY r.ips_session_expires_at LIMIT $2 FOR UPDATE SKIP LOCKED
		), leased AS (
			UPDATE identity_proofing_requests l SET ips_reconcile_leased_until = $1 + make_interval(secs => $3)
			FROM due WHERE l.id = due.id RETURNING l.id
		)
		SELECT ` + requestColumns + requestFrom + `
		WHERE r.id IN (SELECT id FROM leased)
		ORDER BY r.ips_session_expires_at`
	rows, err := s.db.Query(ctx, query, now, limit, deadlineRetry.Seconds())
	if err != nil {
		return nil, fmt.Errorf("proofing: list due requests: %w", err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		req, err := s.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan due request: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: list due requests: %w", err)
	}
	return out, nil
}

// openLinkWhere is a hosted request whose link was never started and is not
// yet seen to lapse.
const openLinkWhere = `r.link_token_hash IS NOT NULL AND r.ips_session_id IS NULL
	AND r.ips_session_ended_at IS NULL AND r.status = 'pending'`

// LapseLinks ends up to limit hosted requests whose link lapsed unstarted, as
// EndSession ends a session: audited identity_proofing.session_ended and sent
// as session.expired, in one transaction. Rows another replica is ending are
// skipped. It reports how many it ended.
func (s *RequestStore) LapseLinks(ctx context.Context, now time.Time, limit int) (int, error) {
	ended := 0
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		rows, err := q.Query(ctx, `WITH lapsed AS (
				SELECT r.id FROM identity_proofing_requests r
				WHERE `+openLinkWhere+` AND r.link_expires_at <= $1
				ORDER BY r.link_expires_at LIMIT $2 FOR UPDATE SKIP LOCKED
			), ended AS (
				UPDATE identity_proofing_requests e SET ips_session_ended_at = now(), updated_at = now()
				FROM lapsed WHERE e.id = lapsed.id RETURNING e.id
			)
			SELECT `+requestColumns+requestFrom+` WHERE r.id IN (SELECT id FROM ended)`, now, limit)
		if err != nil {
			return fmt.Errorf("proofing: lapse links: %w", err)
		}
		var reqs []Request
		for rows.Next() {
			req, err := s.scanRequest(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("proofing: scan lapsed link: %w", err)
			}
			reqs = append(reqs, req)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("proofing: lapse links: %w", err)
		}
		for _, req := range reqs {
			if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionEnded,
				audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
				audit.Updated(
					req.auditFields(map[string]any{"status": string(req.Status)}),
					req.auditFields(map[string]any{"status": string(StatusExpired), "reason": "link_lapsed"}))); err != nil {
				return err
			}
			if err := enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionExpired, &req.ID,
				sessionEventData(req, StatusExpired)); err != nil {
				return err
			}
		}
		ended = len(reqs)
		return nil
	})
	return ended, err
}

// NextDeadline is the earliest session cap or open link's lapse after now;
// zero when there is none.
func (s *RequestStore) NextDeadline(ctx context.Context, now time.Time) (time.Time, error) {
	var next *time.Time
	if err := s.db.QueryRow(ctx, `SELECT least(
			(SELECT min(r.ips_session_expires_at) FROM identity_proofing_requests r
				WHERE `+liveSessionWhere+` AND r.ips_session_expires_at > $1),
			(SELECT min(r.link_expires_at) FROM identity_proofing_requests r
				WHERE `+openLinkWhere+` AND r.link_expires_at > $1))`, now).Scan(&next); err != nil {
		return time.Time{}, fmt.Errorf("proofing: next session deadline: %w", err)
	}
	if next == nil {
		return time.Time{}, nil
	}
	return *next, nil
}

// RecordReviewDecision audits identity_proofing.review_decided: a member's
// decision on a request under review, by the actor in ctx, with its reason.
// The outcome itself lands as any other (RecordOutcome), once IPS settled it.
func (s *RequestStore) RecordReviewDecision(ctx context.Context, req Request, decided Status, reason, errorCode string) error {
	fields := map[string]any{"decision": string(decided), "reason": reason}
	if errorCode != "" {
		fields["errorCode"] = errorCode
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		return s.audit.Record(ctx, q, audit.IdentityProofingReviewDecided,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(fields)))
	})
}

// GetByLinkToken returns the hosted request whose link token hashes to hash.
func (s *RequestStore) GetByLinkToken(ctx context.Context, hash []byte) (Request, error) {
	req, err := s.scanRequest(s.db.QueryRow(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.link_token_hash = $1`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("proofing: request by link: %w", err)
	}
	return req, nil
}

// GetBySession returns the request an IPS session belongs to.
func (s *RequestStore) GetBySession(ctx context.Context, sessionID string) (Request, error) {
	req, err := s.scanRequest(s.db.QueryRow(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.ips_session_id = $1`, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("proofing: request for session %s: %w", sessionID, err)
	}
	return req, nil
}

// Get returns one of the org's requests, or ErrRequestNotFound.
func (s *RequestStore) Get(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	query := `SELECT ` + requestColumns + requestFrom + ` WHERE r.id = $1 AND r.organization_id = $2`
	req, err := s.scanRequest(s.db.QueryRow(ctx, query, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("proofing: get request %s: %w", id, err)
	}
	return req, nil
}

// GetForCustomer returns one of a customer's requests, or ErrRequestNotFound.
func (s *RequestStore) GetForCustomer(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error) {
	query := `SELECT ` + requestColumns + requestFrom + `
		WHERE r.id = $1 AND r.organization_id = $2 AND r.customer_id = $3`
	req, err := s.scanRequest(s.db.QueryRow(ctx, query, id, orgID, customerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("proofing: get request %s: %w", id, err)
	}
	return req, nil
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

// ListPage returns up to limit of a customer's requests, newest first, after
// the cursor when one is given: the customer API's paged list.
func (s *RequestStore) ListPage(ctx context.Context, orgID, customerID uuid.UUID, after *RequestCursor, limit int) ([]Request, error) {
	var afterAt *time.Time
	var afterID *uuid.UUID
	if after != nil {
		afterAt, afterID = &after.CreatedAt, &after.ID
	}
	rows, err := s.db.Query(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.organization_id = $1 AND r.customer_id = $2
			AND ($3::timestamptz IS NULL OR (r.created_at, r.id) < ($3, $4::uuid))
		ORDER BY r.created_at DESC, r.id DESC LIMIT $5`, orgID, customerID, afterAt, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("proofing: page requests customer %s: %w", customerID, err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		req, err := s.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan request customer %s: %w", customerID, err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: page requests customer %s: %w", customerID, err)
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
				flow_version = COALESCE(NULLIF($5, 0), flow_version), method = COALESCE(NULLIF($6, ''), method),
				updated_at = now()
			WHERE id = $1 AND ips_session_id IS NULL AND status = 'pending'`
		tag, err := q.Exec(ctx, update, req.ID, sess.ID, tokenCT, sess.ExpiresAt, sess.FlowVersion, string(req.Method))
		if err != nil {
			return fmt.Errorf("proofing: attach session request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		attached = true
		// Wakes the deadline job for this session's cap.
		if err := database.Notify(ctx, q, SessionChannel); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionCreated,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{
				"flowId": req.FlowID, "flowVersion": sess.FlowVersion, "sessionExpiresAt": sess.ExpiresAt,
			}))); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionCreated, &req.ID,
			sessionEventData(req, StatusPending))
	})
	return attached, err
}

// SetYiviTransaction keeps the verifier transaction of a Yivi request's
// disclosure, replacing a previous one (a restart). ErrSessionOver once the
// request moved to another session or settled.
func (s *RequestStore) SetYiviTransaction(ctx context.Context, req Request, sessionID, transactionID string) error {
	const update = `UPDATE identity_proofing_requests SET yivi_transaction_id = $3, updated_at = now()
		WHERE id = $1 AND ips_session_id = $2 AND status IN ('pending', 'in_progress')`
	tag, err := s.db.Exec(ctx, update, req.ID, sessionID, transactionID)
	if err != nil {
		return fmt.Errorf("proofing: set yivi transaction request %s: %w", req.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionOver
	}
	return nil
}

// MarkStarted records that the subject's phone joined the session (IPS reports
// it opened), and audits identity_proofing.session_started. A no-op when the row
// already moved on, so concurrent pollers record it once.
func (s *RequestStore) MarkStarted(ctx context.Context, req Request, sessionID string, method proofingprovider.Method) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests
			SET status = 'in_progress', method = COALESCE(NULLIF($3, ''), method), updated_at = now()
			WHERE id = $1 AND ips_session_id = $2 AND status = 'pending'`
		tag, err := q.Exec(ctx, update, req.ID, sessionID, string(method))
		if err != nil {
			return fmt.Errorf("proofing: mark started request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionStarted,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(
				req.auditFields(map[string]any{"status": string(req.Status)}),
				req.auditFields(withMethod(map[string]any{"status": string(StatusInProgress)}, method)))); err != nil {
			return err
		}
		started := req
		started.Method = methodOr(method, req.Method)
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionStarted, &req.ID,
			sessionEventData(started, StatusInProgress))
	})
}

// RecordHandover audits identity_proofing.session_handover and sends
// session.handover: a running Idem session was handed a new code for another
// phone, once the one holding it left.
func (s *RequestStore) RecordHandover(ctx context.Context, req Request, expiresAt time.Time) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionHandover,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(withMethod(map[string]any{
				"status": string(req.Status), "claimExpiresAt": expiresAt,
			}, req.Method)))); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionHandover, &req.ID,
			sessionEventData(req, req.Status))
	})
}

// withMethod adds the method a subject used to an audit snapshot, when known.
func withMethod(fields map[string]any, method proofingprovider.Method) map[string]any {
	if method != "" {
		fields["method"] = string(method)
	}
	return fields
}

// EndSession records that the request's IPS session ended without an outcome
// (expired, or cancelled at IPS), which ends the request: it reads as expired
// from then on. Audited identity_proofing.session_ended with IPS's status. A
// no-op once recorded, so concurrent pollers record it once.
func (s *RequestStore) EndSession(ctx context.Context, req Request, sessionID string, ipsStatus proofingprovider.Status, method proofingprovider.Method) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests
			SET ips_session_ended_at = now(), method = COALESCE(NULLIF($3, ''), method), updated_at = now()
			WHERE id = $1 AND ips_session_id = $2 AND ips_session_ended_at IS NULL
				AND status IN ('pending', 'in_progress', 'needs_review')`
		tag, err := q.Exec(ctx, update, req.ID, sessionID, string(method))
		if err != nil {
			return fmt.Errorf("proofing: end session request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionEnded,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(
				req.auditFields(map[string]any{"status": string(req.Status)}),
				req.auditFields(withMethod(map[string]any{"status": string(StatusExpired), "ipsStatus": string(ipsStatus)}, method)))); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionExpired, &req.ID,
			sessionEventData(req, StatusExpired))
	})
}

// Cancel marks a request without an outcome cancelled and its session ended,
// audited identity_proofing.session_cancelled. False when it moved on first.
func (s *RequestStore) Cancel(ctx context.Context, req Request) (bool, error) {
	var done bool
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
			SET cancelled_at = now(), ips_session_ended_at = COALESCE(ips_session_ended_at, now()), updated_at = now()
			WHERE id = $1 AND cancelled_at IS NULL AND purged_at IS NULL AND status IN ('pending', 'in_progress')`, req.ID)
		if err != nil {
			return fmt.Errorf("proofing: cancel request %s: %w", req.ID, err)
		}
		if done = tag.RowsAffected() == 1; !done {
			return nil
		}
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionCancelled,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(req.auditFields(map[string]any{"status": string(req.Status)}),
				req.auditFields(map[string]any{"status": string(StatusCancelled)}))); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionCancelled, &req.ID,
			sessionEventData(req, StatusCancelled))
	})
	return done, err
}

// RecordResultRead audits identity_proofing.result_read: who read a request's
// identity result, never what it said.
func (s *RequestStore) RecordResultRead(ctx context.Context, req Request) error {
	return s.audit.Record(ctx, s.db, audit.IdentityProofingResultRead,
		audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
		map[string]any{"status": string(req.Status)})
}

// Purge erases every personal detail the wallet holds of a request (the
// subject's name and e-mail address, the proofed name, and the subject in its
// audit events) and marks it purged; its outcome stays. Audited
// identity_proofing.session_purged, and sends session.purged. Purging a
// purged request changes nothing.
func (s *RequestStore) Purge(ctx context.Context, req Request) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
			SET purged_at = now(), subject_name = '', subject_email = '',
				proofed_name_ciphertext = NULL, proofed_name_purge_after = NULL,
				ips_session_ended_at = COALESCE(ips_session_ended_at, now()), updated_at = now()
			WHERE id = $1 AND purged_at IS NULL`, req.ID)
		if err != nil {
			return fmt.Errorf("proofing: purge request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE audit_events
			SET metadata = metadata #- '{before,subjectName}' #- '{before,subjectEmail}'
				#- '{after,subjectName}' #- '{after,subjectEmail}'
			WHERE organization_id = $1 AND target_type = $2 AND target_id = $3`,
			req.OrganizationID, audit.TargetIdentityProofingRequest, req.ID.String()); err != nil {
			return fmt.Errorf("proofing: purge audit subject request %s: %w", req.ID, err)
		}
		if err := s.audit.Record(ctx, q, audit.IdentityProofingSessionPurged,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Deleted(map[string]any{"status": string(req.Status)})); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionPurged, &req.ID,
			sessionEventData(req, req.Status))
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
	if email != "" {
		fields["subjectEmail"] = email
	}
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
	// retainSecs is how long the proofed name is kept, from the database's now().
	var retainSecs *float64
	if res.Name != "" {
		if s.cipher == nil {
			return ErrNoEncryptionKey
		}
		var err error
		if nameCT, err = s.cipher.Encrypt([]byte(res.Name)); err != nil {
			return fmt.Errorf("proofing: encrypt proofed name request %s: %w", req.ID, err)
		}
		retention := req.NameRetention
		if retention == 0 {
			retention = ProofedNameRetention
		}
		secs := retention.Seconds()
		retainSecs = &secs
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE identity_proofing_requests SET
				status = $3, assurance_level = NULLIF($4, ''), eidas_level = NULLIF($5, ''),
				error_code = NULLIF($6, ''), completed_at = COALESCE($7, now()), updated_at = now(),
				proofed_name_ciphertext = $8, proofed_name_purge_after = now() + make_interval(secs => $9),
				method = COALESCE(NULLIF($10, ''), method)
			WHERE id = $1 AND ips_session_id = $2
				AND status IN ('pending', 'in_progress', 'needs_review') AND status <> $3`
		tag, err := q.Exec(ctx, update, req.ID, sessionID, string(status),
			res.AssuranceLevel, res.EIDASLevel, res.ErrorCode, res.CompletedAt, nameCT, retainSecs, string(res.Method))
		if err != nil {
			return fmt.Errorf("proofing: record outcome request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		after := withMethod(map[string]any{"status": string(status)}, res.Method)
		for key, value := range map[string]string{
			"assuranceLevel": res.AssuranceLevel, "eidasLevel": res.EIDASLevel, "errorCode": res.ErrorCode,
		} {
			if value != "" {
				after[key] = value
			}
		}
		if err := s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(req.auditFields(map[string]any{"status": string(req.Status)}), req.auditFields(after))); err != nil {
			return err
		}
		event, ok := outcomeEvents[status]
		if !ok {
			return nil
		}
		decided := req
		decided.AssuranceLevel, decided.EIDASLevel, decided.ErrorCode = res.AssuranceLevel, res.EIDASLevel, res.ErrorCode
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, event, &req.ID, sessionEventData(decided, status))
	})
}

// ListPurgeDue returns up to limit requests past their purge time, the
// longest overdue first.
func (s *RequestStore) ListPurgeDue(ctx context.Context, limit int) ([]Request, error) {
	rows, err := s.db.Query(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.purged_at IS NULL AND `+purgeAtExpr+` <= now()
		ORDER BY `+purgeAtExpr+` LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("proofing: list purge due: %w", err)
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		req, err := s.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan purge due: %w", err)
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// Stats counts the live customer requests sent since since, per customer and
// flow, by outcome (test requests are left out); requestedBy narrows them to the requests one member sent. A row
// counts as expired exactly when Request.EffectiveStatus reads it so, and as
// last reconciled: an outcome IPS holds but no list read picked up yet is not in
// it.
func (s *RequestStore) Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID, since time.Time) ([]StatsRow, error) {
	const query = `SELECT customer_id, flow_id, count(*),
			count(*) FILTER (WHERE status = $4),
			count(*) FILTER (WHERE status = $5),
			count(*) FILTER (WHERE status = $6 AND ips_session_ended_at IS NULL),
			count(*) FILTER (WHERE status NOT IN ($4, $5) AND (ips_session_ended_at IS NOT NULL
				OR (status <> $6 AND ((ips_session_id IS NULL
						AND (link_token_hash IS NULL OR link_expires_at <= now()))
					OR (ips_session_id IS NOT NULL
						AND (ips_session_expires_at IS NULL OR ips_session_expires_at <= now()))))))
		FROM identity_proofing_requests
		WHERE organization_id = $1 AND customer_id IS NOT NULL AND mode = 'live'
			AND ($2::uuid IS NULL OR requested_by = $2) AND created_at >= $3
		GROUP BY customer_id, flow_id
		ORDER BY customer_id, flow_id`
	rows, err := s.db.Query(ctx, query, orgID, requestedBy, since,
		StatusApproved, StatusRejected, StatusNeedsReview)
	if err != nil {
		return nil, fmt.Errorf("proofing: stats org %s: %w", orgID, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (StatsRow, error) {
		var r StatsRow
		err := row.Scan(&r.CustomerID, &r.FlowID, &r.Sessions, &r.Approved, &r.Rejected, &r.NeedsReview, &r.Expired)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("proofing: stats org %s: %w", orgID, err)
	}
	return out, nil
}
