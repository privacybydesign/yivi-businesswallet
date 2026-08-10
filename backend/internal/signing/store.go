package signing

import (
	"context"
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

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// UpsertCredential caches a user's linked signing credential (id, certificate +
// chain as PEM, key algo) and audits the link.
func (s *Store) UpsertCredential(ctx context.Context, orgID, userID uuid.UUID, cred signingprovider.Credential) error {
	certPEM := encodeCertPEM(cred.Certificate)
	chainPEM := encodeChainPEM(cred.Chain)
	keyAlgo := strings.Join(cred.KeyAlgo, ",")

	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const upsert = `INSERT INTO signing_credentials
			(organization_id, user_id, credential_id, certificate_pem, chain_pem, key_algo)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (organization_id, user_id) DO UPDATE SET
				credential_id = EXCLUDED.credential_id, certificate_pem = EXCLUDED.certificate_pem,
				chain_pem = EXCLUDED.chain_pem, key_algo = EXCLUDED.key_algo, updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, userID, cred.ID, certPEM, chainPEM, keyAlgo); err != nil {
			return fmt.Errorf("signing: upsert credential org %s user %s: %w", orgID, userID, err)
		}
		return s.audit.Record(ctx, q, audit.SigningCredentialLinked,
			audit.Target{Type: audit.TargetSigningCredential, ID: cred.ID, OrgID: &orgID},
			audit.Created(map[string]any{"credentialId": cred.ID, "keyAlgo": keyAlgo}))
	})
}

// GetCredential returns a user's linked credential, or ErrNoCredential if none.
func (s *Store) GetCredential(ctx context.Context, orgID, userID uuid.UUID) (signingprovider.Credential, error) {
	const query = `SELECT credential_id, certificate_pem, chain_pem, key_algo
		FROM signing_credentials WHERE organization_id = $1 AND user_id = $2`
	var credID, certPEM, chainPEM, keyAlgo string
	err := s.db.QueryRow(ctx, query, orgID, userID).Scan(&credID, &certPEM, &chainPEM, &keyAlgo)
	if errors.Is(err, pgx.ErrNoRows) {
		return signingprovider.Credential{}, ErrNoCredential
	}
	if err != nil {
		return signingprovider.Credential{}, fmt.Errorf("signing: get credential org %s user %s: %w", orgID, userID, err)
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
// rows, and audits it. signers must be non-empty and their orders set (1-based).
func (s *Store) CreateRequest(ctx context.Context, orgID, createdBy uuid.UUID, filename string, pdf []byte, mode string, signers []SignerInput, rec RecipientInput) (uuid.UUID, error) {
	id := uuid.New()
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
		const insertSigner = `INSERT INTO signing_request_signers (request_id, user_id, sign_order, status)
			VALUES ($1,$2,$3,$4)`
		for _, sg := range signers {
			if _, err := q.Exec(ctx, insertSigner, id, sg.UserID, sg.Order, SignerPending); err != nil {
				return fmt.Errorf("signing: create request signer: %w", err)
			}
		}
		return s.audit.Record(ctx, q, audit.SigningRequested,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"filename": filename, "mode": mode, "signers": len(signers),
				"recipientChannel": rec.Channel,
			}))
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
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
// audits signing.signed, and reports whether every signer has now signed.
func (s *Store) RecordSignature(ctx context.Context, orgID, id, userID uuid.UUID, signed []byte) (bool, error) {
	var allSigned bool
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const updateDoc = `UPDATE signing_requests SET signed_document=$2, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, updateDoc, id, signed); err != nil {
			return fmt.Errorf("signing: record signed document %s: %w", id, err)
		}
		const markSigner = `UPDATE signing_request_signers SET status=$3, signed_at=now()
			WHERE request_id=$1 AND user_id=$2`
		tag, err := q.Exec(ctx, markSigner, id, userID, SignerSigned)
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
			audit.Updated(map[string]any{"signer": userID.String(), "status": SignerPending},
				map[string]any{"signer": userID.String(), "status": SignerSigned}))
	})
	return allSigned, err
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

// MarkSignerFailed marks one signer's attempt failed with a redaction-safe reason
// and audits it. It deliberately does NOT touch the request status: an error (or
// timeout) on one signer must leave the request awaiting_signatures and retryable,
// not brick a co-signing request that may already carry other qualified signatures.
// A failed signer is offered the document again (see ListPendingForUser), and a
// successful retry flips them to signed.
func (s *Store) MarkSignerFailed(ctx context.Context, orgID, id, signerID uuid.UUID, reason string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const markSigner = `UPDATE signing_request_signers SET status=$3 WHERE request_id=$1 AND user_id=$2`
		if _, err := q.Exec(ctx, markSigner, id, signerID, SignerFailed); err != nil {
			return fmt.Errorf("signing: mark signer failed %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.SigningFailed,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"signer": signerID.String(), "status": SignerPending},
				map[string]any{"signer": signerID.String(), "status": SignerFailed, "error": reason}))
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

// getSigners loads a request's signer rows (no names) ordered by sign_order.
func (s *Store) getSigners(ctx context.Context, id uuid.UUID) ([]Signer, error) {
	const query = `SELECT user_id, sign_order, status, signed_at
		FROM signing_request_signers WHERE request_id=$1 ORDER BY sign_order, user_id`
	rows, err := s.db.Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("signing: list signers %s: %w", id, err)
	}
	defer rows.Close()
	var out []Signer
	for rows.Next() {
		var sg Signer
		if err := rows.Scan(&sg.UserID, &sg.Order, &sg.Status, &sg.SignedAt); err != nil {
			return nil, fmt.Errorf("signing: scan signer: %w", err)
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

// ListPendingForUser returns the awaiting-signature requests where userID still
// has to sign and it is their turn: their own signer row is not yet signed (pending
// or a failed attempt they can retry), and for sequential mode every signer with a
// lower sign_order has already signed. Newest first.
func (s *Store) ListPendingForUser(ctx context.Context, orgID, userID uuid.UUID) ([]Request, error) {
	const query = `
		SELECT r.id, r.status, r.filename, r.signing_mode, r.created_by, r.recipient_channel,
			r.recipient_name, r.recipient_address, r.message, r.delivery_status, r.delivery_error,
			r.error, r.created_at, r.updated_at
		FROM signing_requests r
		JOIN signing_request_signers me ON me.request_id = r.id AND me.user_id = $2
		WHERE r.organization_id = $1
			AND r.status = $3
			AND me.status <> $4
			AND (
				r.signing_mode = $5
				OR NOT EXISTS (
					SELECT 1 FROM signing_request_signers earlier
					WHERE earlier.request_id = r.id
						AND earlier.sign_order < me.sign_order
						AND earlier.status <> $4
				)
			)
		ORDER BY r.created_at DESC, r.id DESC`
	rows, err := s.db.Query(ctx, query, orgID, userID, StatusAwaitingSignatures,
		SignerSigned, ModeParallel)
	if err != nil {
		return nil, fmt.Errorf("signing: list pending for user %s: %w", userID, err)
	}
	return s.scanRequestList(ctx, rows)
}

// Page-size bounds for ListRequests. This is the single authority that clamps the
// history page size — the handler passes the raw ?limit through (0 when unset or
// unparseable) and this brings it into range.
const (
	defaultPageLimit = 25
	maxPageLimit     = 100
)

// ListRequests returns the org's signing requests, newest first, cursor-paginated.
// An empty cursor starts at the newest; the returned cursor is empty when there is
// no further page.
func (s *Store) ListRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
	if limit <= 0 || limit > maxPageLimit {
		limit = defaultPageLimit
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
	if len(requests) == 0 {
		return requests, nil
	}
	// Load all signers in one query rather than one per request (the history page
	// and the 2s To-sign poll would otherwise be N+1).
	ids := make([]uuid.UUID, len(requests))
	for i := range requests {
		ids[i] = requests[i].ID
	}
	byRequest, err := s.signersFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range requests {
		requests[i].Signers = byRequest[requests[i].ID]
	}
	return requests, nil
}

// signersFor loads the signer rows for many requests in one query, grouped by
// request id (ordered by sign_order within each).
func (s *Store) signersFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Signer, error) {
	const query = `SELECT request_id, user_id, sign_order, status, signed_at
		FROM signing_request_signers WHERE request_id = ANY($1) ORDER BY request_id, sign_order, user_id`
	rows, err := s.db.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("signing: list signers for %d requests: %w", len(ids), err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]Signer, len(ids))
	for rows.Next() {
		var requestID uuid.UUID
		var sg Signer
		if err := rows.Scan(&requestID, &sg.UserID, &sg.Order, &sg.Status, &sg.SignedAt); err != nil {
			return nil, fmt.Errorf("signing: scan signer: %w", err)
		}
		out[requestID] = append(out[requestID], sg)
	}
	return out, rows.Err()
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
