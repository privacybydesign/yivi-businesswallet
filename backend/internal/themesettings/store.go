package themesettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists per-org theme settings.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// GetSettings returns an org's theme (Configured false when no row exists).
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT primary_color, accent_color, logo_uri, updated_at
		FROM org_theme_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.PrimaryColor, &out.AccentColor, &out.LogoURI, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Configured: false}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("themesettings: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	return out, nil
}

// Upsert creates or updates an org's theme and audits, in one transaction.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const upsert = `INSERT INTO org_theme_settings
			(organization_id, primary_color, accent_color, logo_uri)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (organization_id) DO UPDATE SET
				primary_color = EXCLUDED.primary_color,
				accent_color = EXCLUDED.accent_color,
				logo_uri = EXCLUDED.logo_uri,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.PrimaryColor, in.AccentColor, in.LogoURI); err != nil {
			return fmt.Errorf("themesettings: upsert settings org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.ThemeSettingsUpdated,
			audit.Target{Type: audit.TargetThemeSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{
				"primaryColor": in.PrimaryColor,
				"accentColor":  in.AccentColor,
				"hasLogo":      in.LogoURI != "",
			}))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}
