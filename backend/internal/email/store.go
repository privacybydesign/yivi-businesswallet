package email

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

// Store persists per-org SMTP settings. The password is encrypted at rest with
// the deployment e-mail key (`cipher`); when cipher is nil the deployment has no
// e-mail key configured and password storage is rejected.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *Store {
	return &Store{db: db, audit: recorder, cipher: cipher}
}

// GetSettings returns the non-secret view of an org's SMTP settings.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT host, port, username, auth_mechanism, tenant_id, client_id,
		from_name, from_address, enabled,
		password_ciphertext IS NOT NULL, client_secret_ciphertext IS NOT NULL, updated_at
		FROM org_email_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.Host, &out.Port, &out.Username, &out.AuthMechanism, &out.TenantID, &out.ClientID,
		&out.FromName, &out.FromAddress, &out.Enabled,
		&out.HasPassword, &out.HasClientSecret, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// An org that has never saved settings still gets the default mechanism, so
		// the settings screen opens on a selected option rather than a blank one.
		return Settings{Configured: false, AuthMechanism: mailer.AuthPlain}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("email: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	return out, nil
}

// secretArg turns an optional input secret into (value, set) for the upsert: a
// nil secret leaves the stored ciphertext alone, a pointer to "" clears it, and
// anything else is encrypted. Carrying "set" separately is what keeps "leave it
// alone" from being confused with "clear it".
func (s *Store) secretArg(secret *string, name string) (any, bool, error) {
	if secret == nil {
		return nil, false, nil
	}
	if *secret == "" {
		return nil, true, nil
	}
	if s.cipher == nil {
		return nil, false, fmt.Errorf("email: no encryption key configured; cannot store %s", name)
	}
	ct, err := s.cipher.Encrypt([]byte(*secret))
	if err != nil {
		return nil, false, err
	}
	return ct, true, nil
}

// Upsert creates or updates an org's SMTP settings and audits, in one tx. A nil
// input password preserves the stored one; a non-nil password is (re)encrypted;
// an empty non-nil password clears it. The OAuth client secret follows the same
// three-way rule.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	passwordArg, setPassword, err := s.secretArg(in.Password, "password")
	if err != nil {
		return Settings{}, err
	}
	clientSecretArg, setClientSecret, err := s.secretArg(in.ClientSecret, "client secret")
	if err != nil {
		return Settings{}, err
	}
	mechanism := in.AuthMechanism
	if mechanism == "" {
		mechanism = mailer.AuthPlain
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		// When setPassword is false, keep the existing ciphertext on conflict; on
		// insert it defaults to NULL. Same for the client secret.
		const upsert = `INSERT INTO org_email_settings
			(organization_id, host, port, username, auth_mechanism, tenant_id, client_id,
			 from_name, from_address, enabled, password_ciphertext, client_secret_ciphertext)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (organization_id) DO UPDATE SET
				host = EXCLUDED.host, port = EXCLUDED.port, username = EXCLUDED.username,
				auth_mechanism = EXCLUDED.auth_mechanism, tenant_id = EXCLUDED.tenant_id,
				client_id = EXCLUDED.client_id,
				from_name = EXCLUDED.from_name, from_address = EXCLUDED.from_address,
				enabled = EXCLUDED.enabled,
				password_ciphertext = CASE WHEN $13 THEN EXCLUDED.password_ciphertext
				                           ELSE org_email_settings.password_ciphertext END,
				client_secret_ciphertext = CASE WHEN $14 THEN EXCLUDED.client_secret_ciphertext
				                                ELSE org_email_settings.client_secret_ciphertext END,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.Host, in.Port, in.Username, string(mechanism),
			in.TenantID, in.ClientID, in.FromName, in.FromAddress, in.Enabled,
			passwordArg, clientSecretArg, setPassword, setClientSecret); err != nil {
			return fmt.Errorf("email: upsert settings org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.EmailSettingsUpdated,
			audit.Target{Type: audit.TargetEmailSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{
				"host": in.Host, "fromAddress": in.FromAddress, "enabled": in.Enabled,
				"authMechanism": string(mechanism),
			}))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// configFor resolves everything one send needs (including the decrypted secrets)
// for sending. ok is false when there is no row or it is disabled.
func (s *Store) configFor(ctx context.Context, orgID uuid.UUID) (sendConfig, bool, error) {
	const query = `SELECT host, port, username, auth_mechanism, tenant_id, client_id,
		from_name, from_address, enabled, password_ciphertext, client_secret_ciphertext
		FROM org_email_settings WHERE organization_id = $1`
	var (
		out          sendConfig
		enabled      bool
		passwordCT   []byte
		clientSecret []byte
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.Mailer.Host, &out.Mailer.Port, &out.Mailer.Username, &out.Mailer.AuthMechanism,
		&out.OAuth.TenantID, &out.OAuth.ClientID,
		&out.Mailer.FromName, &out.Mailer.FromAddress, &enabled, &passwordCT, &clientSecret,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return sendConfig{}, false, nil
	}
	if err != nil {
		return sendConfig{}, false, fmt.Errorf("email: config for org %s: %w", orgID, err)
	}
	if !enabled {
		return sendConfig{}, false, nil
	}
	password, err := s.decryptSecret(passwordCT, "password")
	if err != nil {
		return sendConfig{}, false, err
	}
	out.Mailer.Password = password
	secret, err := s.decryptSecret(clientSecret, "client secret")
	if err != nil {
		return sendConfig{}, false, err
	}
	out.OAuth.ClientSecret = secret
	return out, true, nil
}

// decryptSecret opens one stored ciphertext, or returns "" when the column is
// null. A stored secret with no deployment key is a misconfiguration, not an
// absent secret, so it is an error rather than a silent empty credential.
func (s *Store) decryptSecret(ciphertext []byte, name string) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("email: %s stored but no encryption key configured", name)
	}
	plain, err := s.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
