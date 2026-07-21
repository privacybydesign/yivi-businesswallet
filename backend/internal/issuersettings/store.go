package issuersettings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ErrNoLogo is returned by GetLogo when the org has no stored logo.
var ErrNoLogo = errors.New("issuersettings: no logo")

// Store persists per-org issuer instance settings.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// GetSettings returns an org's issuer settings (Configured false when no row).
// The logo bytes are not read here — only whether a logo is stored (HasLogo);
// the handler turns that into the served LogoURI.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT instance_name, display_name, logo_bytes IS NOT NULL, enabled, updated_at
		FROM org_issuer_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.InstanceName, &out.DisplayName, &out.HasLogo, &out.Enabled, &out.UpdatedAt,
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

// GetLogo returns the org's stored logo, or ErrNoLogo when none is set.
func (s *Store) GetLogo(ctx context.Context, orgID uuid.UUID) (Logo, error) {
	const query = `SELECT logo_bytes, logo_content_type
		FROM org_issuer_settings WHERE organization_id = $1`
	var logo Logo
	err := s.db.QueryRow(ctx, query, orgID).Scan(&logo.Bytes, &logo.ContentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Logo{}, ErrNoLogo
	}
	if err != nil {
		return Logo{}, fmt.Errorf("issuersettings: get logo org %s: %w", orgID, err)
	}
	if len(logo.Bytes) == 0 {
		return Logo{}, ErrNoLogo
	}
	return logo, nil
}

// Upsert creates or updates an org's issuer settings and, when logo.Replace is
// set, its logo, then audits — all in one transaction.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput, logo LogoUpdate) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if logo.Replace {
			// An empty Logo (nil bytes) clears the stored logo.
			var bytes []byte
			if len(logo.Logo.Bytes) > 0 {
				bytes = logo.Logo.Bytes
			}
			const upsert = `INSERT INTO org_issuer_settings
				(organization_id, instance_name, display_name, enabled, logo_bytes, logo_content_type)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (organization_id) DO UPDATE SET
					instance_name = EXCLUDED.instance_name,
					display_name = EXCLUDED.display_name,
					enabled = EXCLUDED.enabled,
					logo_bytes = EXCLUDED.logo_bytes,
					logo_content_type = EXCLUDED.logo_content_type,
					updated_at = now()`
			if _, err := q.Exec(ctx, upsert, orgID, in.InstanceName, in.DisplayName, in.Enabled, bytes, logo.Logo.ContentType); err != nil {
				return fmt.Errorf("issuersettings: upsert settings org %s: %w", orgID, err)
			}
		} else {
			const upsert = `INSERT INTO org_issuer_settings
				(organization_id, instance_name, display_name, enabled)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (organization_id) DO UPDATE SET
					instance_name = EXCLUDED.instance_name,
					display_name = EXCLUDED.display_name,
					enabled = EXCLUDED.enabled,
					updated_at = now()`
			if _, err := q.Exec(ctx, upsert, orgID, in.InstanceName, in.DisplayName, in.Enabled); err != nil {
				return fmt.Errorf("issuersettings: upsert settings org %s: %w", orgID, err)
			}
		}

		after := map[string]any{"instanceName": in.InstanceName, "enabled": in.Enabled}
		if logo.Replace {
			after["hasLogo"] = len(logo.Logo.Bytes) > 0
		}
		return s.audit.Record(ctx, q, audit.IssuerSettingsUpdated,
			audit.Target{Type: audit.TargetIssuerSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, after))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// BundleConfig returns the values needed to generate an org's issuer GitOps
// bundle: the instance name (defaulted to fallbackInstance when the org has no
// row) plus its display-name / logo branding. The logo is returned as a
// self-contained data: URI because the hosted issuer serves the generated
// metadata to wallets and cannot reach the business-wallet logo endpoint.
func (s *Store) BundleConfig(ctx context.Context, orgID uuid.UUID, fallbackInstance string) (instance, displayName, logoURI string, err error) {
	settings, err := s.GetSettings(ctx, orgID)
	if err != nil {
		return "", "", "", err
	}

	logoURI = ""
	if settings.HasLogo {
		logo, err := s.GetLogo(ctx, orgID)
		if err != nil && !errors.Is(err, ErrNoLogo) {
			return "", "", "", err
		}
		if err == nil {
			logoURI = logoDataURI(logo)
		}
	}

	instance = settings.InstanceName
	if !settings.Configured || settings.InstanceName == "" {
		instance = fallbackInstance
	}
	return instance, settings.DisplayName, logoURI, nil
}

// logoDataURI encodes a stored logo as an RFC 2397 data: URI so the generated
// issuer metadata carries the image inline.
func logoDataURI(logo Logo) string {
	return fmt.Sprintf("data:%s;base64,%s", logo.ContentType, base64.StdEncoding.EncodeToString(logo.Bytes))
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
