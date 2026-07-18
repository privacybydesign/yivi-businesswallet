// Package wsca stores each organization's WSCA (Wallet Secure Cryptographic
// Application) account state for the business wallet's holder-binding keys.
//
// A self-managing business wallet signs headlessly (see
// .ai/features/wsca-holder-binding.md), so the SECDSA activation secret — the
// knowledge factor the WSCA requires on every sign — is sealed at rest under the
// deployment KEK and decrypted only in-memory at sign time. The WSCA account id
// (hex(sha256(DER(U)))) is stable across secret rotation; a fresh certificate id
// is recorded on activation and rotation.
package wsca

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ErrNotConfigured is returned when the deployment has no WSCA KEK configured, so
// a secret can neither be sealed nor opened.
var ErrNotConfigured = errors.New("wsca: key-encryption key not configured")

// ErrNotActivated is returned when an org has no WSCA account yet.
var ErrNotActivated = errors.New("wsca: organization has no activated WSCA account")

// Account is the non-secret view of an org's WSCA account.
type Account struct {
	OrganizationID uuid.UUID  `json:"organizationId"`
	AccountID      string     `json:"accountId"`
	CertificateID  string     `json:"certificateId"`
	ActivatedAt    time.Time  `json:"activatedAt"`
	RotatedAt      *time.Time `json:"rotatedAt,omitempty"`
}

// Store persists per-org WSCA account state. The activation secret is sealed with
// the deployment WSCA key (cipher); when cipher is nil, WSCA is not configured
// and secret operations return ErrNotConfigured.
type Store struct {
	db     database.DB
	cipher *crypto.Cipher
}

func NewStore(db database.DB, cipher *crypto.Cipher) *Store {
	return &Store{db: db, cipher: cipher}
}

// Configured reports whether a WSCA KEK is set (so secrets can be sealed/opened).
func (s *Store) Configured() bool { return s.cipher != nil }

// Activate records (or replaces) the org's WSCA account after a successful
// walletmobile activation: it seals secret and stores it with the account and
// certificate ids. Re-activating an org resets rotated_at.
func (s *Store) Activate(ctx context.Context, orgID uuid.UUID, secret, accountID, certificateID string) (Account, error) {
	if s.cipher == nil {
		return Account{}, ErrNotConfigured
	}
	ct, err := s.cipher.Encrypt([]byte(secret))
	if err != nil {
		return Account{}, fmt.Errorf("wsca: seal secret org %s: %w", orgID, err)
	}
	const query = `INSERT INTO org_wsca_accounts
		(organization_id, account_id, certificate_id, secret_ciphertext)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id) DO UPDATE SET
			account_id = EXCLUDED.account_id,
			certificate_id = EXCLUDED.certificate_id,
			secret_ciphertext = EXCLUDED.secret_ciphertext,
			activated_at = now(),
			rotated_at = NULL,
			updated_at = now()
		RETURNING organization_id, account_id, certificate_id, activated_at, rotated_at`
	return scanAccount(s.db.QueryRow(ctx, query, orgID, accountID, certificateID, ct))
}

// Rotate reseals a new secret for an already-activated org (the account id is
// unchanged; a WSCA ChangePIN issues a fresh certificate) and stamps rotated_at.
func (s *Store) Rotate(ctx context.Context, orgID uuid.UUID, newSecret, certificateID string) (Account, error) {
	if s.cipher == nil {
		return Account{}, ErrNotConfigured
	}
	ct, err := s.cipher.Encrypt([]byte(newSecret))
	if err != nil {
		return Account{}, fmt.Errorf("wsca: seal rotated secret org %s: %w", orgID, err)
	}
	const query = `UPDATE org_wsca_accounts SET
			certificate_id = $2,
			secret_ciphertext = $3,
			rotated_at = now(),
			updated_at = now()
		WHERE organization_id = $1
		RETURNING organization_id, account_id, certificate_id, activated_at, rotated_at`
	acc, err := scanAccount(s.db.QueryRow(ctx, query, orgID, certificateID, ct))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotActivated
	}
	return acc, err
}

// Secret returns the decrypted activation secret for a sign operation. Callers
// MUST NOT log it. Returns ErrNotActivated when the org has no account.
func (s *Store) Secret(ctx context.Context, orgID uuid.UUID) (string, error) {
	if s.cipher == nil {
		return "", ErrNotConfigured
	}
	const query = `SELECT secret_ciphertext FROM org_wsca_accounts WHERE organization_id = $1`
	var ct []byte
	if err := s.db.QueryRow(ctx, query, orgID).Scan(&ct); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotActivated
		}
		return "", fmt.Errorf("wsca: read secret org %s: %w", orgID, err)
	}
	pt, err := s.cipher.Decrypt(ct)
	if err != nil {
		return "", fmt.Errorf("wsca: open secret org %s: %w", orgID, err)
	}
	return string(pt), nil
}

// Get returns the non-secret account view. Returns ErrNotActivated when absent.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID) (Account, error) {
	const query = `SELECT organization_id, account_id, certificate_id, activated_at, rotated_at
		FROM org_wsca_accounts WHERE organization_id = $1`
	acc, err := scanAccount(s.db.QueryRow(ctx, query, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotActivated
	}
	return acc, err
}

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	if err := row.Scan(&a.OrganizationID, &a.AccountID, &a.CertificateID, &a.ActivatedAt, &a.RotatedAt); err != nil {
		return Account{}, err
	}
	return a, nil
}
