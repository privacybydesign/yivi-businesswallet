package issuersettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists per-org issuer instance settings.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// GetSettings returns an org's issuer settings (Configured false when no row).
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT instance_name, display_name, logo_uri, enabled, updated_at
		FROM org_issuer_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.InstanceName, &out.DisplayName, &out.LogoURI, &out.Enabled, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Configured: false}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("issuersettings: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	return out, nil
}

// Upsert creates or updates an org's issuer settings and audits, in one tx.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const upsert = `INSERT INTO org_issuer_settings
			(organization_id, instance_name, display_name, logo_uri, enabled)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (organization_id) DO UPDATE SET
				instance_name = EXCLUDED.instance_name,
				display_name = EXCLUDED.display_name,
				logo_uri = EXCLUDED.logo_uri,
				enabled = EXCLUDED.enabled,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.InstanceName, in.DisplayName, in.LogoURI, in.Enabled); err != nil {
			return fmt.Errorf("issuersettings: upsert settings org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IssuerSettingsUpdated,
			audit.Target{Type: audit.TargetIssuerSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"instanceName": in.InstanceName, "enabled": in.Enabled}))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// BundleConfig returns the values needed to generate an org's issuer GitOps
// bundle: the instance name (defaulted to fallbackInstance when the org has no
// row) plus its display-name / logo branding.
func (s *Store) BundleConfig(ctx context.Context, orgID uuid.UUID, fallbackInstance string) (instance, displayName, logoURI string, err error) {
	settings, err := s.GetSettings(ctx, orgID)
	if err != nil {
		return "", "", "", err
	}
	if !settings.Configured || settings.InstanceName == "" {
		return fallbackInstance, settings.DisplayName, settings.LogoURI, nil
	}
	return settings.InstanceName, settings.DisplayName, settings.LogoURI, nil
}

// InstanceFor resolves an org's issuer instance name so attestation offers route
// to that org's issuer. Returns "" when the org has no (or a disabled) instance,
// signalling the caller to use the deployment's default instance.
func (s *Store) InstanceFor(ctx context.Context, orgID uuid.UUID) (string, error) {
	const query = `SELECT instance_name FROM org_issuer_settings
		WHERE organization_id = $1 AND enabled = true`
	var instance string
	err := s.db.QueryRow(ctx, query, orgID).Scan(&instance)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("issuersettings: instance for org %s: %w", orgID, err)
	}
	return instance, nil
}
