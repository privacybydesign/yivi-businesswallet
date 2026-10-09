package proofing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	// apiKeyPrefix starts every customer API key, so a leaked one is
	// recognisable (and scannable) as ours.
	apiKeyPrefix = "yp_live_"
	// apiKeySecretBytes is the random part of a key.
	apiKeySecretBytes = 32
	// apiKeyShownPrefix is how much of a key stays visible after creation.
	apiKeyShownPrefix = len(apiKeyPrefix) + 4
	// maxAPIKeyNameLength bounds the label an admin gives a key.
	maxAPIKeyNameLength = 100
)

// API key scopes: what a customer key may call.
const (
	ScopeSessionsWrite = "sessions:write"
	ScopeSessionsRead  = "sessions:read"
	ScopeResultsRead   = "results:read"
	ScopeFlowsRead     = "flows:read"
)

// APIKeyScopes are every scope, in display order; every new key gets them all.
var APIKeyScopes = []string{ScopeSessionsWrite, ScopeSessionsRead, ScopeResultsRead, ScopeFlowsRead}

// APIKey is one of a customer's API keys, without its secret.
type APIKey struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	CustomerID     uuid.UUID
	Name           string
	Prefix         string
	Scopes         []string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
}

// APIKeyActorPrefix starts the audit actor label of an API-key caller, which
// the key's prefix follows.
const APIKeyActorPrefix = "api_key:"

// APIKeyCaller is who an authenticated API key acts for: the key, its customer
// and the customer's org.
type APIKeyCaller struct {
	KeyID      uuid.UUID
	KeyName    string
	KeyPrefix  string
	Scopes     []string
	CustomerID uuid.UUID
	Org        Org
	// LastUsedAt is when the key was last recorded used (Touch); nil never.
	LastUsedAt *time.Time
}

// CustomerScope is what a customer API key reaches: its customer's requests.
type CustomerScope struct {
	OrgID      uuid.UUID
	CustomerID uuid.UUID
}

// Scope is the requests c reaches.
func (c APIKeyCaller) Scope() CustomerScope {
	return CustomerScope{OrgID: c.Org.ID, CustomerID: c.CustomerID}
}

// APIKeyStore persists customer API keys, hashed. Creating and revoking one is
// audited.
type APIKeyStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewAPIKeyStore(db database.DB, recorder audit.Recorder) *APIKeyStore {
	return &APIKeyStore{db: db, audit: recorder}
}

func newAPIKeySecret() (raw string, hash [sha256.Size]byte) {
	b := make([]byte, apiKeySecretBytes)
	_, _ = rand.Read(b)
	raw = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw))
}

const apiKeyColumns = `id, organization_id, customer_id, name, prefix, scopes, created_at, last_used_at, revoked_at`

func scanAPIKey(row pgx.CollectableRow) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.OrganizationID, &k.CustomerID, &k.Name, &k.Prefix, &k.Scopes,
		&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	return k, err
}

// Create stores a new key for a customer and returns it with its secret, which
// is never readable again. Audited identity_proofing.api_key_created.
func (s *APIKeyStore) Create(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string, scopes []string) (APIKey, string, error) {
	raw, hash := newAPIKeySecret()
	prefix := raw[:apiKeyShownPrefix]
	var id uuid.UUID
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := getCustomer(ctx, q, orgID, customerID); err != nil {
			return err
		}
		if err := q.QueryRow(ctx, `INSERT INTO identity_proofing_api_keys
			(organization_id, customer_id, name, prefix, secret_hash, created_by, scopes)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			orgID, customerID, name, prefix, hash[:], createdBy, scopes).Scan(&id); err != nil {
			return fmt.Errorf("proofing: create api key customer %s: %w", customerID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingAPIKeyCreated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"apiKeyId": id.String(), "name": name, "prefix": prefix, "scopes": scopes,
			}))
	})
	if err != nil {
		return APIKey{}, "", err
	}
	key, err := s.get(ctx, orgID, customerID, id)
	return key, raw, err
}

func (s *APIKeyStore) get(ctx context.Context, orgID, customerID, id uuid.UUID) (APIKey, error) {
	rows, err := s.db.Query(ctx, `SELECT `+apiKeyColumns+` FROM identity_proofing_api_keys
		WHERE organization_id = $1 AND customer_id = $2 AND id = $3`, orgID, customerID, id)
	if err != nil {
		return APIKey{}, fmt.Errorf("proofing: read api key %s: %w", id, err)
	}
	key, err := pgx.CollectExactlyOneRow(rows, scanAPIKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrAPIKeyNotFound
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("proofing: read api key %s: %w", id, err)
	}
	return key, nil
}

// List returns a customer's keys, revoked ones included, oldest first.
func (s *APIKeyStore) List(ctx context.Context, orgID, customerID uuid.UUID) ([]APIKey, error) {
	rows, err := s.db.Query(ctx, `SELECT `+apiKeyColumns+` FROM identity_proofing_api_keys
		WHERE organization_id = $1 AND customer_id = $2 ORDER BY created_at, id`, orgID, customerID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list api keys customer %s: %w", customerID, err)
	}
	out, err := pgx.CollectRows(rows, scanAPIKey)
	if err != nil {
		return nil, fmt.Errorf("proofing: list api keys customer %s: %w", customerID, err)
	}
	return out, nil
}

// Revoke stops a key authenticating and audits identity_proofing.api_key_revoked.
// Revoking a revoked key changes and audits nothing.
func (s *APIKeyStore) Revoke(ctx context.Context, orgID, customerID, id uuid.UUID) (APIKey, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var name, prefix string
		err := q.QueryRow(ctx, `UPDATE identity_proofing_api_keys SET revoked_at = now()
			WHERE organization_id = $1 AND customer_id = $2 AND id = $3 AND revoked_at IS NULL
			RETURNING name, prefix`, orgID, customerID, id).Scan(&name, &prefix)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("proofing: revoke api key %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingAPIKeyRevoked,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"apiKeyId": id.String(), "name": name, "prefix": prefix}))
	})
	if err != nil {
		return APIKey{}, err
	}
	return s.get(ctx, orgID, customerID, id)
}

// Authenticate resolves a raw key to the key, its customer and org: a read,
// so an unknown key costs no write (Touch records the use). An unknown,
// malformed or revoked key is ErrAPIKeyInvalid.
func (s *APIKeyStore) Authenticate(ctx context.Context, raw string) (APIKeyCaller, error) {
	if !strings.HasPrefix(raw, apiKeyPrefix) {
		return APIKeyCaller{}, ErrAPIKeyInvalid
	}
	hash := sha256.Sum256([]byte(raw))
	var c APIKeyCaller
	err := s.db.QueryRow(ctx, `SELECT k.id, k.name, k.prefix, k.scopes, k.customer_id, o.id, o.name, k.last_used_at
		FROM identity_proofing_api_keys k JOIN organizations o ON o.id = k.organization_id
		WHERE k.secret_hash = $1 AND k.revoked_at IS NULL`, hash[:]).
		Scan(&c.KeyID, &c.KeyName, &c.KeyPrefix, &c.Scopes, &c.CustomerID, &c.Org.ID, &c.Org.Name, &c.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKeyCaller{}, ErrAPIKeyInvalid
	}
	if err != nil {
		return APIKeyCaller{}, fmt.Errorf("proofing: authenticate api key: %w", err)
	}
	return c, nil
}

// apiKeyTouchEvery is how stale a key's last_used_at may get: a key in use is
// written at most this often, not on every call.
const apiKeyTouchEvery = time.Minute

// Touch records caller's key as used now, unless that was recorded within
// apiKeyTouchEvery.
func (s *APIKeyStore) Touch(ctx context.Context, caller APIKeyCaller) error {
	if caller.LastUsedAt != nil && time.Since(*caller.LastUsedAt) < apiKeyTouchEvery {
		return nil
	}
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_api_keys SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - $2::interval)`,
		caller.KeyID, apiKeyTouchEvery.String()); err != nil {
		return fmt.Errorf("proofing: touch api key %s: %w", caller.KeyID, err)
	}
	return nil
}
