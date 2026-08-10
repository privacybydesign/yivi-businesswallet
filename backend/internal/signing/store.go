package signing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// Store persists per-user linked signing credentials and co-signing requests. The
// original upload and the accumulating signed document are stored on the request
// row; no key material is ever held (the QTSP owns the key), only the public
// certificate chain and produced signature.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

// externalTokenBytes is the entropy behind an external signee's invitation link —
// their only credential for reaching a document they were never given an account
// for, so it matches the org-invitation token (internal/organization).
const externalTokenBytes = 32

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// subject names whose signing credential a store call is about: an internal member
// (a row in users) or an external signee (an e-mail address with no users row).
// Exactly one of the two is set, which is what the signing_credentials CHECK
// enforces at the other end.
type subject struct {
	userID uuid.UUID // uuid.Nil for an external signee
	email  string    // "" for an internal member
}

func internalSubject(userID uuid.UUID) subject { return subject{userID: userID} }

// externalSubject keys an external signee by their address, lower-cased so the
// same person is one subject however they (or the requester) typed it.
func externalSubject(email string) subject {
	return subject{email: strings.ToLower(strings.TrimSpace(email))}
}

func (s subject) isExternal() bool { return s.userID == uuid.Nil }

// String describes the subject for an error or audit entry.
func (s subject) String() string {
	if s.isExternal() {
		return s.email
	}
	return s.userID.String()
}

// UpsertCredential caches a signer's linked signing credential (id, certificate +
// chain as PEM, key algo) and audits the link. The subject is an internal member or
// an external signee; neither shares a row with the other.
func (s *Store) UpsertCredential(ctx context.Context, orgID uuid.UUID, subj subject, cred signingprovider.Credential) error {
	certPEM := encodeCertPEM(cred.Certificate)
	chainPEM := encodeChainPEM(cred.Chain)
	keyAlgo := strings.Join(cred.KeyAlgo, ",")

	return database.InTx(ctx, s.db, func(q database.Querier) error {
		// The two subject kinds have their own partial unique index, so each needs its
		// own conflict target; a single ON CONFLICT cannot name both.
		const upsertMember = `INSERT INTO signing_credentials
			(organization_id, user_id, credential_id, certificate_pem, chain_pem, key_algo)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (organization_id, user_id) WHERE user_id IS NOT NULL DO UPDATE SET
				credential_id = EXCLUDED.credential_id, certificate_pem = EXCLUDED.certificate_pem,
				chain_pem = EXCLUDED.chain_pem, key_algo = EXCLUDED.key_algo, updated_at = now()`
		const upsertExternal = `INSERT INTO signing_credentials
			(organization_id, external_email, credential_id, certificate_pem, chain_pem, key_algo)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (organization_id, lower(external_email)) WHERE user_id IS NULL DO UPDATE SET
				credential_id = EXCLUDED.credential_id, certificate_pem = EXCLUDED.certificate_pem,
				chain_pem = EXCLUDED.chain_pem, key_algo = EXCLUDED.key_algo, updated_at = now()`
		upsert, key := upsertMember, any(subj.userID)
		if subj.isExternal() {
			upsert, key = upsertExternal, subj.email
		}
		if _, err := q.Exec(ctx, upsert, orgID, key, cred.ID, certPEM, chainPEM, keyAlgo); err != nil {
			return fmt.Errorf("signing: upsert credential org %s subject %s: %w", orgID, subj, err)
		}
		return s.audit.Record(ctx, q, audit.SigningCredentialLinked,
			audit.Target{Type: audit.TargetSigningCredential, ID: cred.ID, OrgID: &orgID},
			audit.Created(map[string]any{"credentialId": cred.ID, "keyAlgo": keyAlgo}))
	})
}

// GetCredential returns a subject's linked credential, or ErrNoCredential if none.
func (s *Store) GetCredential(ctx context.Context, orgID uuid.UUID, subj subject) (signingprovider.Credential, error) {
	const selectMember = `SELECT credential_id, certificate_pem, chain_pem, key_algo
		FROM signing_credentials WHERE organization_id = $1 AND user_id = $2`
	const selectExternal = `SELECT credential_id, certificate_pem, chain_pem, key_algo
		FROM signing_credentials WHERE organization_id = $1 AND user_id IS NULL AND lower(external_email) = $2`
	query, key := selectMember, any(subj.userID)
	if subj.isExternal() {
		query, key = selectExternal, subj.email
	}
	var credID, certPEM, chainPEM, keyAlgo string
	err := s.db.QueryRow(ctx, query, orgID, key).Scan(&credID, &certPEM, &chainPEM, &keyAlgo)
	if errors.Is(err, pgx.ErrNoRows) {
		return signingprovider.Credential{}, ErrNoCredential
	}
	if err != nil {
		return signingprovider.Credential{}, fmt.Errorf("signing: get credential org %s subject %s: %w", orgID, subj, err)
	}
	chain, err := decodeChainPEM(chainPEM)
	if err != nil {
		return signingprovider.Credential{}, err
	}
	if len(chain) == 0 {
		return signingprovider.Credential{}, fmt.Errorf("signing: stored credential %s has no certificate", credID)
	}
	return signingprovider.Credential{ID: credID, Certificate: chain[0], Chain: chain, KeyAlgo: splitAlgo(keyAlgo)}, nil
}

// CreateRequest records a new co-signing request (awaiting signatures), its signer
// rows, and audits it. signers must be non-empty and their orders set (1-based). It
// returns the request id and the persisted signer rows — the caller needs their row
// ids to invite the external signees, who have no user id to be addressed by.
func (s *Store) CreateRequest(ctx context.Context, orgID, createdBy uuid.UUID, filename string, pdf []byte, mode string, signers []SignerInput, rec RecipientInput) (uuid.UUID, []Signer, error) {
	id := uuid.New()
	created := make([]Signer, 0, len(signers))
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO signing_requests
			(id, organization_id, created_by, status, filename, original_document, signing_mode,
			 recipient_channel, recipient_address, recipient_name, message, delivery_status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
		deliveryStatus := DeliveryNotRequested
		if rec.Channel != ChannelNone {
			deliveryStatus = DeliveryPending
		}
		if _, err := q.Exec(ctx, insert, id, orgID, createdBy, StatusAwaitingSignatures, filename, pdf, mode,
			rec.Channel, rec.Address, rec.Name, rec.Message, deliveryStatus); err != nil {
			return fmt.Errorf("signing: create request org %s: %w", orgID, err)
		}
		const insertSigner = `INSERT INTO signing_request_signers
			(request_id, user_id, external_email, external_name, sign_order, status)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`
		external := 0
		for _, sg := range signers {
			var userID *uuid.UUID
			email, name := "", ""
			if sg.Kind == KindExternal {
				email, name = sg.Email, sg.Name
				external++
			} else {
				userID = &sg.UserID
			}
			row := Signer{
				Kind: sg.Kind, UserID: userID, Email: email, Name: name,
				Order: sg.Order, Status: SignerPending,
			}
			if err := q.QueryRow(ctx, insertSigner, id, userID, email, name, sg.Order, SignerPending).
				Scan(&row.ID); err != nil {
				return fmt.Errorf("signing: create request signer: %w", err)
			}
			created = append(created, row)
		}
		return s.audit.Record(ctx, q, audit.SigningRequested,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"filename": filename, "mode": mode, "signers": len(signers),
				"externalSigners": external, "recipientChannel": rec.Channel,
			}))
	})
	if err != nil {
		return uuid.Nil, nil, err
	}
	return id, created, nil
}

// GetLatestDocument returns the current document to sign next: the accumulating
// signed_document if a prior signer has signed, else the original upload, plus the
// filename. Scoped by org.
func (s *Store) GetLatestDocument(ctx context.Context, orgID, id uuid.UUID) ([]byte, string, error) {
	const query = `SELECT filename, COALESCE(signed_document, original_document)
		FROM signing_requests WHERE id=$1 AND organization_id=$2`
	var filename string
	var doc []byte
	err := s.db.QueryRow(ctx, query, id, orgID).Scan(&filename, &doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("signing: get latest document %s: %w", id, err)
	}
	if len(doc) == 0 {
		return nil, "", ErrInvalidPDF
	}
	return doc, filename, nil
}

// RecordSignature persists the new signed document, marks the acting signer signed,
// audits signing.signed, and reports whether every signer has now signed. signerID
// is the signer row's own id, so it addresses an internal member and an external
// signee alike. An external signee's invitation token is deliberately left in place:
// the ceremony returns them to that link, and it is where they see that their
// signature landed and can re-read what they signed until it expires. What stops it
// being replayed is their signer status, not deleting the row's token.
func (s *Store) RecordSignature(ctx context.Context, orgID, id, signerID uuid.UUID, signed []byte) (bool, error) {
	var allSigned bool
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const updateDoc = `UPDATE signing_requests SET signed_document=$2, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, updateDoc, id, signed); err != nil {
			return fmt.Errorf("signing: record signed document %s: %w", id, err)
		}
		const markSigner = `UPDATE signing_request_signers SET status=$3, signed_at=now()
			WHERE request_id=$1 AND id=$2`
		tag, err := q.Exec(ctx, markSigner, id, signerID, SignerSigned)
		if err != nil {
			return fmt.Errorf("signing: mark signer signed %s: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotSigner
		}
		const remaining = `SELECT count(*) FROM signing_request_signers
			WHERE request_id=$1 AND status <> $2`
		var pending int
		if err := q.QueryRow(ctx, remaining, id, SignerSigned).Scan(&pending); err != nil {
			return fmt.Errorf("signing: count remaining signers %s: %w", id, err)
		}
		allSigned = pending == 0
		return s.audit.Record(ctx, q, audit.SigningSigned,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"signer": signerID.String(), "status": SignerPending},
				map[string]any{"signer": signerID.String(), "status": SignerSigned}))
	})
	return allSigned, err
}

// IssueExternalToken mints an external signee's one-time invitation token, storing
// only its hash plus an ExternalInviteTTL expiry, and returns the raw token for the
// invitation mail. Issuing again replaces the previous token, so a link is only live
// while that signee is actually being asked.
func (s *Store) IssueExternalToken(ctx context.Context, requestID, signerID uuid.UUID) (string, error) {
	raw, hash, err := newExternalToken()
	if err != nil {
		return "", err
	}
	const update = `UPDATE signing_request_signers
		SET invite_token_hash=$3, invite_expires_at=$4
		WHERE request_id=$1 AND id=$2 AND user_id IS NULL`
	tag, err := s.db.Exec(ctx, update, requestID, signerID, hash[:], time.Now().Add(ExternalInviteTTL))
	if err != nil {
		return "", fmt.Errorf("signing: issue external token %s: %w", signerID, err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotSigner
	}
	return raw, nil
}

// externalSigner is an external signee resolved from their invitation token: which
// org and request they were invited to, and which signer row they are.
type externalSigner struct {
	OrgID     uuid.UUID
	RequestID uuid.UUID
	SignerID  uuid.UUID
	Email     string
	Name      string
}

// SignerByToken resolves an external signee's invitation token to their signer row,
// or ErrInvalidToken when the token is unknown, spent or expired. The token is
// looked up by hash — the raw value only ever exists in the invitation mail.
func (s *Store) SignerByToken(ctx context.Context, rawToken string) (externalSigner, error) {
	if strings.TrimSpace(rawToken) == "" {
		return externalSigner{}, ErrInvalidToken
	}
	hash := hashExternalToken(rawToken)
	const query = `SELECT r.organization_id, sg.request_id, sg.id, sg.external_email, sg.external_name
		FROM signing_request_signers sg
		JOIN signing_requests r ON r.id = sg.request_id
		WHERE sg.invite_token_hash = $1 AND sg.invite_expires_at > now()`
	var out externalSigner
	err := s.db.QueryRow(ctx, query, hash[:]).
		Scan(&out.OrgID, &out.RequestID, &out.SignerID, &out.Email, &out.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return externalSigner{}, ErrInvalidToken
	}
	if err != nil {
		return externalSigner{}, fmt.Errorf("signing: resolve signing link: %w", err)
	}
	return out, nil
}

// CompleteRequest marks the request completed (all signers signed) and audits it.
func (s *Store) CompleteRequest(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE signing_requests SET status=$2, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, update, id, StatusCompleted); err != nil {
			return fmt.Errorf("signing: complete request %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.SigningCompleted,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": StatusAwaitingSignatures}, map[string]any{"status": StatusCompleted}))
	})
}

// SetDelivery updates the delivery status (and redaction-safe error) after a
// completed request's document is dispatched to the recipient; audits a delivery.
func (s *Store) SetDelivery(ctx context.Context, orgID, id uuid.UUID, status, reason string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE signing_requests SET delivery_status=$2, delivery_error=$3, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, update, id, status, reason); err != nil {
			return fmt.Errorf("signing: set delivery %s: %w", id, err)
		}
		if status != DeliveryDelivered {
			return nil
		}
		return s.audit.Record(ctx, q, audit.SigningDelivered,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"deliveryStatus": DeliveryPending}, map[string]any{"deliveryStatus": DeliveryDelivered}))
	})
}

// FailRequest marks a request (and, when set, the acting signer row) failed with a
// redaction-safe reason and audits it.
func (s *Store) FailRequest(ctx context.Context, orgID, id, signerID uuid.UUID, reason string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE signing_requests SET status=$2, error=$3, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, update, id, StatusFailed, reason); err != nil {
			return fmt.Errorf("signing: fail request %s: %w", id, err)
		}
		if signerID != uuid.Nil {
			const markSigner = `UPDATE signing_request_signers SET status=$3 WHERE request_id=$1 AND id=$2`
			if _, err := q.Exec(ctx, markSigner, id, signerID, SignerFailed); err != nil {
				return fmt.Errorf("signing: mark signer failed %s: %w", id, err)
			}
		}
		return s.audit.Record(ctx, q, audit.SigningFailed,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": StatusAwaitingSignatures}, map[string]any{"status": StatusFailed, "error": reason}))
	})
}

// GetRequest returns a request's metadata (no document bytes) plus its signer rows
// (without names — the service enriches those from the member directory). Scoped by
// org; caller-level authorization (creator/signer/admin) is enforced above.
func (s *Store) GetRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	const query = `SELECT id, status, filename, signing_mode, created_by, recipient_channel,
			recipient_name, recipient_address, message, delivery_status, delivery_error, error, created_at, updated_at
		FROM signing_requests WHERE id=$1 AND organization_id=$2`
	req, updatedAt, err := scanRequest(s.db.QueryRow(ctx, query, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("signing: get request %s: %w", id, err)
	}
	if req.Status == StatusCompleted {
		req.CompletedAt = &updatedAt
	}
	signers, err := s.getSigners(ctx, id)
	if err != nil {
		return Request{}, err
	}
	req.Signers = signers
	return req, nil
}

// getSigners loads a request's signer rows ordered by sign_order. An internal
// member's name/email is left to the service to enrich from the member directory; an
// external signee carries their own, since there is no directory to look them up in.
func (s *Store) getSigners(ctx context.Context, id uuid.UUID) ([]Signer, error) {
	const query = `SELECT id, user_id, external_email, external_name, sign_order, status, signed_at
		FROM signing_request_signers WHERE request_id=$1 ORDER BY sign_order, id`
	rows, err := s.db.Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("signing: list signers %s: %w", id, err)
	}
	defer rows.Close()
	var out []Signer
	for rows.Next() {
		var sg Signer
		var email, name string
		if err := rows.Scan(&sg.ID, &sg.UserID, &email, &name, &sg.Order, &sg.Status, &sg.SignedAt); err != nil {
			return nil, fmt.Errorf("signing: scan signer: %w", err)
		}
		sg.Kind = KindInternal
		if sg.UserID == nil {
			sg.Kind, sg.Email, sg.Name = KindExternal, email, name
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// GetSignedDocument returns the final signed PDF + filename, or ErrNotCompleted.
func (s *Store) GetSignedDocument(ctx context.Context, orgID, id uuid.UUID) ([]byte, string, error) {
	const query = `SELECT status, filename, signed_document
		FROM signing_requests WHERE id=$1 AND organization_id=$2`
	var status, filename string
	var doc []byte
	err := s.db.QueryRow(ctx, query, id, orgID).Scan(&status, &filename, &doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("signing: get signed document %s: %w", id, err)
	}
	if status != StatusCompleted || len(doc) == 0 {
		return nil, "", ErrNotCompleted
	}
	return doc, filename, nil
}

// ListPendingForUser returns the awaiting-signature requests where userID is a
// still-pending signer whose turn it is: for sequential mode, every signer with a
// lower sign_order must already have signed. Newest first.
func (s *Store) ListPendingForUser(ctx context.Context, orgID, userID uuid.UUID) ([]Request, error) {
	const query = `
		SELECT r.id, r.status, r.filename, r.signing_mode, r.created_by, r.recipient_channel,
			r.recipient_name, r.recipient_address, r.message, r.delivery_status, r.delivery_error,
			r.error, r.created_at, r.updated_at
		FROM signing_requests r
		JOIN signing_request_signers me ON me.request_id = r.id AND me.user_id = $2
		WHERE r.organization_id = $1
			AND r.status = $3
			AND me.status = $4
			AND (
				r.signing_mode = $5
				OR NOT EXISTS (
					SELECT 1 FROM signing_request_signers earlier
					WHERE earlier.request_id = r.id
						AND earlier.sign_order < me.sign_order
						AND earlier.status <> $6
				)
			)
		ORDER BY r.created_at DESC, r.id DESC`
	rows, err := s.db.Query(ctx, query, orgID, userID, StatusAwaitingSignatures,
		SignerPending, ModeParallel, SignerSigned)
	if err != nil {
		return nil, fmt.Errorf("signing: list pending for user %s: %w", userID, err)
	}
	return s.scanRequestList(ctx, rows)
}

// ListRequests returns the org's signing requests, newest first, cursor-paginated.
// An empty cursor starts at the newest; the returned cursor is empty when there is
// no further page.
func (s *Store) ListRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	curTime, curID, hasCursor, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	var rows pgx.Rows
	const cols = `r.id, r.status, r.filename, r.signing_mode, r.created_by, r.recipient_channel,
		r.recipient_name, r.recipient_address, r.message, r.delivery_status, r.delivery_error,
		r.error, r.created_at, r.updated_at`
	if hasCursor {
		//nolint:gosec // limit is clamped to [1,100] above.
		query := `SELECT ` + cols + ` FROM signing_requests r
			WHERE r.organization_id=$1 AND (r.created_at, r.id) < ($2, $3)
			ORDER BY r.created_at DESC, r.id DESC LIMIT ` + strconv.Itoa(limit+1)
		rows, err = s.db.Query(ctx, query, orgID, curTime, curID)
	} else {
		//nolint:gosec // limit is clamped to [1,100] above.
		query := `SELECT ` + cols + ` FROM signing_requests r
			WHERE r.organization_id=$1
			ORDER BY r.created_at DESC, r.id DESC LIMIT ` + strconv.Itoa(limit+1)
		rows, err = s.db.Query(ctx, query, orgID)
	}
	if err != nil {
		return nil, "", fmt.Errorf("signing: list requests org %s: %w", orgID, err)
	}
	requests, err := s.scanRequestList(ctx, rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(requests) > limit {
		last := requests[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		requests = requests[:limit]
	}
	return requests, next, nil
}

// scanRequestList scans request rows, then loads each request's signers. rows is
// closed here.
func (s *Store) scanRequestList(ctx context.Context, rows pgx.Rows) ([]Request, error) {
	var requests []Request
	for rows.Next() {
		req, updatedAt, err := scanRequest(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("signing: scan request: %w", err)
		}
		if req.Status == StatusCompleted {
			req.CompletedAt = &updatedAt
		}
		requests = append(requests, req)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range requests {
		signers, err := s.getSigners(ctx, requests[i].ID)
		if err != nil {
			return nil, err
		}
		requests[i].Signers = signers
	}
	return requests, nil
}

// scanRow is the subset of pgx.Row/pgx.Rows scanRequest reads from.
type scanRow interface {
	Scan(dest ...any) error
}

// scanRequest scans one request row (in the fixed column order used above) and
// returns the request plus its updated_at (used to derive CompletedAt).
func scanRequest(row scanRow) (Request, time.Time, error) {
	var r Request
	var updatedAt time.Time
	err := row.Scan(&r.ID, &r.Status, &r.Filename, &r.Mode, &r.CreatedBy, &r.RecipientChannel,
		&r.RecipientName, &r.RecipientAddress, &r.Message, &r.DeliveryStatus, &r.DeliveryError,
		&r.Error, &r.CreatedAt, &updatedAt)
	return r, updatedAt, err
}

// encodeCursor / decodeCursor keyset-encode (created_at, id) for stable, gap-free
// pagination independent of inserts.
func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := strconv.FormatInt(t.UTC().UnixNano(), 10) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, bool, error) {
	if cursor == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidRequest
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false, ErrInvalidRequest
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidRequest
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidRequest
	}
	return time.Unix(0, nanos).UTC(), id, true, nil
}

// newExternalToken mints an external signee's invitation token: the raw value for
// the mail and the SHA-256 hash that is all the database keeps, so a leaked dump
// cannot be replayed as a signing link. Same construction as an org invitation
// (internal/organization).
func newExternalToken() (string, [sha256.Size]byte, error) {
	b := make([]byte, externalTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("signing: external signing token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashExternalToken(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}

func encodeCertPEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

func encodeChainPEM(chain []*x509.Certificate) string {
	var b strings.Builder
	for _, c := range chain {
		b.WriteString(encodeCertPEM(c))
	}
	return b.String()
}

func decodeChainPEM(s string) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	rest := []byte(s)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signing: decode stored certificate: %w", err)
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

func splitAlgo(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
