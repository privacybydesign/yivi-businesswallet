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
	const query = `SELECT host, port, username, from_name, from_address, enabled,
		password_ciphertext IS NOT NULL, updated_at
		FROM org_email_settings WHERE organization_id = $1`
	var (
		out         Settings
		hasPassword bool
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.Host, &out.Port, &out.Username, &out.FromName, &out.FromAddress,
		&out.Enabled, &hasPassword, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Configured: false}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("email: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	out.HasPassword = hasPassword
	return out, nil
}

// Upsert creates or updates an org's SMTP settings and audits, in one tx. A nil
// input password preserves the stored one; a non-nil password is (re)encrypted;
// an empty non-nil password clears it.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	var passwordArg any // nil => keep existing (COALESCE), []byte => set, sentinel empty => clear
	setPassword := false
	if in.Password != nil {
		setPassword = true
		if *in.Password == "" {
			passwordArg = nil
		} else {
			if s.cipher == nil {
				return Settings{}, fmt.Errorf("email: no encryption key configured; cannot store password")
			}
			ct, err := s.cipher.Encrypt([]byte(*in.Password))
			if err != nil {
				return Settings{}, err
			}
			passwordArg = ct
		}
	}

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// When setPassword is false, keep the existing ciphertext via COALESCE with
		// the current value on conflict; on insert it defaults to NULL.
		const upsert = `INSERT INTO org_email_settings
			(organization_id, host, port, username, from_name, from_address, enabled, password_ciphertext)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (organization_id) DO UPDATE SET
				host = EXCLUDED.host, port = EXCLUDED.port, username = EXCLUDED.username,
				from_name = EXCLUDED.from_name, from_address = EXCLUDED.from_address,
				enabled = EXCLUDED.enabled,
				password_ciphertext = CASE WHEN $9 THEN EXCLUDED.password_ciphertext
				                           ELSE org_email_settings.password_ciphertext END,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.Host, in.Port, in.Username, in.FromName, in.FromAddress, in.Enabled, passwordArg, setPassword); err != nil {
			return fmt.Errorf("email: upsert settings org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.EmailSettingsUpdated,
			audit.Target{Type: audit.TargetEmailSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"host": in.Host, "fromAddress": in.FromAddress, "enabled": in.Enabled}))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// configFor resolves the full SMTP config (including the decrypted password) for
// sending. ok is false when there is no row or it is disabled.
func (s *Store) configFor(ctx context.Context, orgID uuid.UUID) (mailer.Config, bool, error) {
	const query = `SELECT host, port, username, from_name, from_address, enabled, password_ciphertext
		FROM org_email_settings WHERE organization_id = $1`
	var (
		cfg     mailer.Config
		enabled bool
		ct      []byte
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&cfg.Host, &cfg.Port, &cfg.Username, &cfg.FromName, &cfg.FromAddress, &enabled, &ct,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailer.Config{}, false, nil
	}
	if err != nil {
		return mailer.Config{}, false, fmt.Errorf("email: config for org %s: %w", orgID, err)
	}
	if !enabled {
		return mailer.Config{}, false, nil
	}
	if len(ct) > 0 {
		if s.cipher == nil {
			return mailer.Config{}, false, fmt.Errorf("email: password stored but no encryption key configured")
		}
		pw, err := s.cipher.Decrypt(ct)
		if err != nil {
			return mailer.Config{}, false, err
		}
		cfg.Password = string(pw)
	}
	return cfg, true, nil
}
