package yivicsc

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store keeps the service's certificate authority and the signing credentials
// it issued. Every private key is sealed under the deployment's CSC key before
// it is written; nothing here holds one in the clear.
type Store struct {
	db     database.DB
	cipher *crypto.Cipher
}

// NewStore returns a Store. cipher must not be nil: without a key the service
// is off (ErrUnavailable) and no Store is built.
func NewStore(db database.DB, cipher *crypto.Cipher) *Store {
	return &Store{db: db, cipher: cipher}
}

// credential is an issued signing credential: its key, its certificate and the
// subject it belongs to.
type credential struct {
	id         uuid.UUID
	subjectKey []byte
	source     string
	cert       *x509.Certificate
	key        *ecdsa.PrivateKey
}

// Authority returns the deployment's certificate authority, making it on first
// use. Two replicas starting at once both make one; the insert keeps the first
// and both read that one back.
func (s *Store) authority(ctx context.Context, now time.Time) (*authority, error) {
	a, err := s.readAuthority(ctx)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	fresh, err := newAuthority(now)
	if err != nil {
		return nil, err
	}
	sealed, err := s.sealKey(fresh.key)
	if err != nil {
		return nil, err
	}
	const insert = `INSERT INTO yivi_csc_authority (certificate_der, private_key_ciphertext)
		VALUES ($1, $2) ON CONFLICT (singleton) DO NOTHING`
	if _, err := s.db.Exec(ctx, insert, fresh.cert.Raw, sealed); err != nil {
		return nil, fmt.Errorf("yivicsc: store authority: %w", err)
	}
	return s.readAuthority(ctx)
}

func (s *Store) readAuthority(ctx context.Context) (*authority, error) {
	const query = `SELECT certificate_der, private_key_ciphertext FROM yivi_csc_authority`
	var certDER, sealed []byte
	if err := s.db.QueryRow(ctx, query).Scan(&certDER, &sealed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("yivicsc: read authority: %w", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: parse authority certificate: %w", err)
	}
	key, err := s.openKey(sealed)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: open authority key: %w", err)
	}
	return &authority{cert: cert, key: key}, nil
}

// credentialSelect reads a credential row.
const credentialSelect = `SELECT id, subject_key, source, certificate_der, private_key_ciphertext
	FROM yivi_csc_credentials`

// credentialFor returns the subject's credential still valid at now, issuing
// one with issue when there is none. A subject has one row: an expired
// credential is replaced in place, under a new id, so a certificate the
// ceremony cached is never silently swapped for another.
func (s *Store) credentialFor(ctx context.Context, subject []byte, source string, now time.Time,
	issue func() (*ecdsa.PrivateKey, *x509.Certificate, error),
) (credential, error) {
	const valid = credentialSelect + ` WHERE subject_key = $1 AND not_after > $2`
	c, err := s.scanCredential(s.db.QueryRow(ctx, valid, subject, now))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return credential{}, err
	}

	key, cert, err := issue()
	if err != nil {
		return credential{}, err
	}
	sealed, err := s.sealKey(key)
	if err != nil {
		return credential{}, err
	}
	// Two requests for one new subject both get here. The conflict clause lets
	// the second replace only an expired row, so it leaves the first's fresh
	// credential alone and the read below returns that one to both.
	const upsert = `INSERT INTO yivi_csc_credentials
			(subject_key, source, certificate_der, private_key_ciphertext, not_after)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (subject_key) DO UPDATE SET
			id = gen_random_uuid(), source = EXCLUDED.source,
			certificate_der = EXCLUDED.certificate_der,
			private_key_ciphertext = EXCLUDED.private_key_ciphertext,
			not_after = EXCLUDED.not_after, created_at = now()
		WHERE yivi_csc_credentials.not_after <= $6`
	if _, err := s.db.Exec(ctx, upsert, subject, source, cert.Raw, sealed, cert.NotAfter, now); err != nil {
		return credential{}, fmt.Errorf("yivicsc: store credential: %w", err)
	}
	c, err = s.scanCredential(s.db.QueryRow(ctx, valid, subject, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return credential{}, ErrCredentialNotFound
	}
	return c, err
}

// credentialByID returns a credential by its id, valid or not; the caller
// decides what an expired one may still do.
func (s *Store) credentialByID(ctx context.Context, id uuid.UUID) (credential, error) {
	const query = credentialSelect + ` WHERE id = $1`
	c, err := s.scanCredential(s.db.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return credential{}, ErrCredentialNotFound
	}
	return c, err
}

func (s *Store) scanCredential(row pgx.Row) (credential, error) {
	var c credential
	var certDER, sealed []byte
	if err := row.Scan(&c.id, &c.subjectKey, &c.source, &certDER, &sealed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credential{}, err
		}
		return credential{}, fmt.Errorf("yivicsc: read credential: %w", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return credential{}, fmt.Errorf("yivicsc: parse credential certificate: %w", err)
	}
	key, err := s.openKey(sealed)
	if err != nil {
		return credential{}, fmt.Errorf("yivicsc: open credential key: %w", err)
	}
	c.cert, c.key = cert, key
	return c, nil
}

func (s *Store) sealKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: encode key: %w", err)
	}
	sealed, err := s.cipher.Encrypt(der)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: seal key: %w", err)
	}
	return sealed, nil
}

func (s *Store) openKey(sealed []byte) (*ecdsa.PrivateKey, error) {
	der, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return nil, err
	}
	return x509.ParseECPrivateKey(der)
}
