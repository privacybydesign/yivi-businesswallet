package signing

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// Store persists per-user linked signing credentials and signing requests. The
// signed document is stored on the request row; no key material is ever held (the
// QTSP owns the key), only the public certificate chain and produced signature.
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

// CreateRequest records a new signing request (awaiting authorization) and audits it.
func (s *Store) CreateRequest(ctx context.Context, orgID, userID uuid.UUID, credentialID, filename string) (uuid.UUID, error) {
	id := uuid.New()
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO signing_requests
			(id, organization_id, user_id, status, credential_id, filename)
			VALUES ($1,$2,$3,$4,$5,$6)`
		if _, err := q.Exec(ctx, insert, id, orgID, userID, StatusAwaitingAuthorization, credentialID, filename); err != nil {
			return fmt.Errorf("signing: create request org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.SigningRequested,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{"filename": filename, "credentialId": credentialID}))
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// CompleteRequest stores the signed document and marks the request completed.
func (s *Store) CompleteRequest(ctx context.Context, orgID, id uuid.UUID, signed []byte) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE signing_requests SET status=$2, signed_document=$3, updated_at=now()
			WHERE id=$1`
		if _, err := q.Exec(ctx, update, id, StatusCompleted, signed); err != nil {
			return fmt.Errorf("signing: complete request %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.SigningCompleted,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": StatusAwaitingAuthorization}, map[string]any{"status": StatusCompleted}))
	})
}

// FailRequest marks a request failed with a redaction-safe reason and audits it.
func (s *Store) FailRequest(ctx context.Context, orgID, id uuid.UUID, reason string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE signing_requests SET status=$2, error=$3, updated_at=now() WHERE id=$1`
		if _, err := q.Exec(ctx, update, id, StatusFailed, reason); err != nil {
			return fmt.Errorf("signing: fail request %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.SigningFailed,
			audit.Target{Type: audit.TargetSigningRequest, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": StatusAwaitingAuthorization}, map[string]any{"status": StatusFailed, "error": reason}))
	})
}

// GetRequest returns a request's metadata (no document bytes), scoped to the owner.
func (s *Store) GetRequest(ctx context.Context, orgID, userID, id uuid.UUID) (Request, error) {
	const query = `SELECT id, status, filename, credential_id, error, created_at, updated_at
		FROM signing_requests WHERE id=$1 AND organization_id=$2 AND user_id=$3`
	var r Request
	var errText string
	var updatedAt time.Time
	err := s.db.QueryRow(ctx, query, id, orgID, userID).Scan(
		&r.ID, &r.Status, &r.Filename, &r.CredentialID, &errText, &r.CreatedAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("signing: get request %s: %w", id, err)
	}
	r.Error = errText
	if r.Status == StatusCompleted {
		r.CompletedAt = &updatedAt
	}
	return r, nil
}

// GetSignedDocument returns the signed PDF + filename, or ErrNotCompleted.
func (s *Store) GetSignedDocument(ctx context.Context, orgID, userID, id uuid.UUID) ([]byte, string, error) {
	const query = `SELECT status, filename, signed_document
		FROM signing_requests WHERE id=$1 AND organization_id=$2 AND user_id=$3`
	var status, filename string
	var doc []byte
	err := s.db.QueryRow(ctx, query, id, orgID, userID).Scan(&status, &filename, &doc)
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
