package csc

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists per-org CSC provider settings. The client secret is encrypted at
// rest with the deployment CSC key (`cipher`); when cipher is nil the deployment
// has no key and storing a secret is refused rather than kept in the clear.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *Store {
	return &Store{db: db, audit: recorder, cipher: cipher}
}

// GetSettings returns the non-secret view of an org's CSC settings.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT provider_kind, base_url, client_id,
		client_secret_ciphertext IS NOT NULL, enabled, updated_at
		FROM org_csc_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.ProviderKind, &out.BaseURL, &out.ClientID, &out.HasClientSecret, &out.Enabled, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Configured: false, ProviderKind: ProviderKindSample}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("csc: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	return out, nil
}

// Upsert replaces an org's CSC settings with the (already normalized) input and
// audits the change in the same transaction. A nil input secret preserves the
// stored one, a non-nil secret is (re)encrypted, and a non-nil empty secret clears
// it. Returns ErrNoEncryptionKey when a secret is offered and the deployment has
// no CSC key. `enabled` is clamped off when no base URL is stored, because a
// provider with no endpoint cannot be reached — the settings screen refuses that
// state too, and this is where both sides are made to agree.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	var secretArg any // nil => clear (or, with setSecret false, keep the stored one)
	setSecret := false
	if in.ClientSecret != nil {
		setSecret = true
		if *in.ClientSecret != "" {
			if s.cipher == nil {
				return Settings{}, ErrNoEncryptionKey
			}
			ciphertext, err := s.cipher.Encrypt([]byte(*in.ClientSecret))
			if err != nil {
				return Settings{}, fmt.Errorf("csc: encrypt client secret org %s: %w", orgID, err)
			}
			secretArg = ciphertext
		}
	}

	// A provider with no endpoint cannot be enabled: there is nothing to reach.
	enabled := in.Enabled && in.BaseURL != ""

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Read (and lock) the current row inside the transaction so the audited
		// {before, after} diff is the change this save actually made.
		before, err := readAuditable(ctx, q, orgID)
		if err != nil {
			return err
		}

		const upsert = `INSERT INTO org_csc_settings
			(organization_id, provider_kind, base_url, client_id, enabled, client_secret_ciphertext)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization_id) DO UPDATE SET
				provider_kind = EXCLUDED.provider_kind, base_url = EXCLUDED.base_url,
				client_id = EXCLUDED.client_id, enabled = EXCLUDED.enabled,
				client_secret_ciphertext = CASE WHEN $7 THEN EXCLUDED.client_secret_ciphertext
				                                ELSE org_csc_settings.client_secret_ciphertext END,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, string(in.ProviderKind), in.BaseURL,
			in.ClientID, enabled, secretArg, setSecret); err != nil {
			return fmt.Errorf("csc: upsert settings org %s: %w", orgID, err)
		}

		// hasClientSecret in the audited state: a secret is stored if this save set a
		// non-empty one, or if one was already stored and this save left it alone.
		hasSecret := before.hasClientSecret
		if setSecret {
			hasSecret = secretArg != nil
		}
		after := auditSnapshot(in.ProviderKind, in.BaseURL, in.ClientID, enabled, hasSecret)
		// The secret itself is never audited: an audit event is read back by every org
		// admin and exported. Whether one is set is what a reader needs.
		return s.audit.Record(ctx, q, audit.CSCSettingsUpdated,
			audit.Target{Type: audit.TargetCSCSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(before.snapshot(), after))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// auditState is the set of facts about an org's CSC settings the audit event
// carries. The secret is never among them, only whether one is stored.
type auditState struct {
	providerKind    ProviderKind
	baseURL         string
	clientID        string
	enabled         bool
	hasClientSecret bool
}

func (a auditState) snapshot() map[string]any {
	return auditSnapshot(a.providerKind, a.baseURL, a.clientID, a.enabled, a.hasClientSecret)
}

func auditSnapshot(kind ProviderKind, baseURL, clientID string, enabled, hasSecret bool) map[string]any {
	return map[string]any{
		"providerKind":    string(kind),
		"baseUrl":         baseURL,
		"clientId":        clientID,
		"enabled":         enabled,
		"hasClientSecret": hasSecret,
	}
}

// readAuditable reads (and locks) an org's current settings for the audit diff. A
// missing row reads as nil, so the first save renders as a create rather than an
// update against an invented empty configuration.
func readAuditable(ctx context.Context, q database.Querier, orgID uuid.UUID) (auditState, error) {
	const query = `SELECT provider_kind, base_url, client_id, enabled,
		client_secret_ciphertext IS NOT NULL
		FROM org_csc_settings WHERE organization_id = $1 FOR UPDATE`
	var current auditState
	switch err := q.QueryRow(ctx, query, orgID).Scan(
		&current.providerKind, &current.baseURL, &current.clientID, &current.enabled, &current.hasClientSecret); {
	case errors.Is(err, pgx.ErrNoRows):
		return auditState{providerKind: ProviderKindSample}, nil
	case err != nil:
		return auditState{}, fmt.Errorf("csc: read settings org %s: %w", orgID, err)
	}
	return current, nil
}

// ResolveConnection returns an org's CSC base URL, OAuth client id, and the
// DECRYPTED client secret, for the signing ceremony (internal/signing) to drive
// the authorization + token exchange. The secret is decrypted here and never
// leaves as ciphertext; an org with no row returns empty strings (the caller
// treats that as "not configured"). A stored secret with no deployment key is a
// misconfiguration surfaced as an error, not a silent empty secret.
func (s *Store) ResolveConnection(ctx context.Context, orgID uuid.UUID) (baseURL, clientID, clientSecret string, err error) {
	const query = `SELECT base_url, client_id, client_secret_ciphertext
		FROM org_csc_settings WHERE organization_id = $1`
	var ciphertext []byte
	err = s.db.QueryRow(ctx, query, orgID).Scan(&baseURL, &clientID, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", fmt.Errorf("csc: resolve connection org %s: %w", orgID, err)
	}
	if len(ciphertext) > 0 {
		if s.cipher == nil {
			return "", "", "", ErrNoEncryptionKey
		}
		plain, derr := s.cipher.Decrypt(ciphertext)
		if derr != nil {
			return "", "", "", fmt.Errorf("csc: decrypt client secret org %s: %w", orgID, derr)
		}
		clientSecret = string(plain)
	}
	return baseURL, clientID, clientSecret, nil
}

// Available reports whether the org has a CSC signing provider configured and
// enabled. It reads only the non-secret settings view, so it is safe to expose to
// members (it is the gate for showing the signing feature to non-admins, who
// cannot read the full settings).
func (s *Store) Available(ctx context.Context, orgID uuid.UUID) (bool, error) {
	settings, err := s.GetSettings(ctx, orgID)
	if err != nil {
		return false, err
	}
	return settings.Configured && settings.Enabled, nil
}

// BaseURLFor resolves the base URL a connection test should probe. It returns
// ErrNotConfigured when the org has no row or no base URL stored — neither is a
// failure, both are "nothing to test".
func (s *Store) BaseURLFor(ctx context.Context, orgID uuid.UUID) (string, error) {
	const query = `SELECT base_url FROM org_csc_settings WHERE organization_id = $1`
	var baseURL string
	err := s.db.QueryRow(ctx, query, orgID).Scan(&baseURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotConfigured
	}
	if err != nil {
		return "", fmt.Errorf("csc: base url for org %s: %w", orgID, err)
	}
	if baseURL == "" {
		return "", ErrNotConfigured
	}
	return baseURL, nil
}
