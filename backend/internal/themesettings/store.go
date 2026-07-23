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

// ErrNoLogo is returned by GetLogo when the org has no stored logo.
var ErrNoLogo = errors.New("themesettings: no logo")

// Store persists per-org theme settings.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// GetSettings returns an org's theme (Configured false when no row exists). The
// logo bytes are not read here — only whether a logo is stored (HasLogo); the
// handler turns that into the served LogoURI.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT primary_color, accent_color, text_color, surface_color,
			border_color, link_color, success_color, warning_color, error_color,
			sidebar_color, topbar_color, font_family,
			logo_bytes IS NOT NULL, updated_at
		FROM org_theme_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.PrimaryColor, &out.AccentColor, &out.TextColor, &out.SurfaceColor,
		&out.BorderColor, &out.LinkColor, &out.SuccessColor, &out.WarningColor, &out.ErrorColor,
		&out.SidebarColor, &out.TopbarColor, &out.FontFamily,
		&out.HasLogo, &out.UpdatedAt,
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

// GetLogo returns the org's stored logo, or ErrNoLogo when none is set.
func (s *Store) GetLogo(ctx context.Context, orgID uuid.UUID) (Logo, error) {
	const query = `SELECT logo_bytes, logo_content_type
		FROM org_theme_settings WHERE organization_id = $1`
	var logo Logo
	err := s.db.QueryRow(ctx, query, orgID).Scan(&logo.Bytes, &logo.ContentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Logo{}, ErrNoLogo
	}
	if err != nil {
		return Logo{}, fmt.Errorf("themesettings: get logo org %s: %w", orgID, err)
	}
	if len(logo.Bytes) == 0 {
		return Logo{}, ErrNoLogo
	}
	return logo, nil
}

// Save creates or updates an org's colours and, when logo.Replace is set, its
// logo, then audits — all in one transaction.
func (s *Store) Save(ctx context.Context, orgID uuid.UUID, in SettingsInput, logo LogoUpdate) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if logo.Replace {
			// An empty Logo (nil bytes) clears the stored logo.
			var bytes []byte
			if len(logo.Logo.Bytes) > 0 {
				bytes = logo.Logo.Bytes
			}
			const upsert = `INSERT INTO org_theme_settings
				(organization_id, primary_color, accent_color, text_color, surface_color,
					border_color, link_color, success_color, warning_color, error_color,
					sidebar_color, topbar_color, font_family,
					logo_bytes, logo_content_type)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
				ON CONFLICT (organization_id) DO UPDATE SET
					primary_color = EXCLUDED.primary_color,
					accent_color = EXCLUDED.accent_color,
					text_color = EXCLUDED.text_color,
					surface_color = EXCLUDED.surface_color,
					border_color = EXCLUDED.border_color,
					link_color = EXCLUDED.link_color,
					success_color = EXCLUDED.success_color,
					warning_color = EXCLUDED.warning_color,
					error_color = EXCLUDED.error_color,
					sidebar_color = EXCLUDED.sidebar_color,
					topbar_color = EXCLUDED.topbar_color,
					font_family = EXCLUDED.font_family,
					logo_bytes = EXCLUDED.logo_bytes,
					logo_content_type = EXCLUDED.logo_content_type,
					updated_at = now()`
			if _, err := q.Exec(ctx, upsert, orgID, in.PrimaryColor, in.AccentColor,
				in.TextColor, in.SurfaceColor, in.BorderColor, in.LinkColor,
				in.SuccessColor, in.WarningColor, in.ErrorColor,
				in.SidebarColor, in.TopbarColor, in.FontFamily, bytes, logo.Logo.ContentType); err != nil {
				return fmt.Errorf("themesettings: save settings org %s: %w", orgID, err)
			}
		} else {
			const upsert = `INSERT INTO org_theme_settings
				(organization_id, primary_color, accent_color, text_color, surface_color,
					border_color, link_color, success_color, warning_color, error_color,
					sidebar_color, topbar_color, font_family)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
				ON CONFLICT (organization_id) DO UPDATE SET
					primary_color = EXCLUDED.primary_color,
					accent_color = EXCLUDED.accent_color,
					text_color = EXCLUDED.text_color,
					surface_color = EXCLUDED.surface_color,
					border_color = EXCLUDED.border_color,
					link_color = EXCLUDED.link_color,
					success_color = EXCLUDED.success_color,
					warning_color = EXCLUDED.warning_color,
					error_color = EXCLUDED.error_color,
					sidebar_color = EXCLUDED.sidebar_color,
					topbar_color = EXCLUDED.topbar_color,
					font_family = EXCLUDED.font_family,
					updated_at = now()`
			if _, err := q.Exec(ctx, upsert, orgID, in.PrimaryColor, in.AccentColor,
				in.TextColor, in.SurfaceColor, in.BorderColor, in.LinkColor,
				in.SuccessColor, in.WarningColor, in.ErrorColor,
				in.SidebarColor, in.TopbarColor, in.FontFamily); err != nil {
				return fmt.Errorf("themesettings: save settings org %s: %w", orgID, err)
			}
		}

		after := map[string]any{
			"primaryColor": in.PrimaryColor,
			"accentColor":  in.AccentColor,
			"textColor":    in.TextColor,
			"surfaceColor": in.SurfaceColor,
			"borderColor":  in.BorderColor,
			"linkColor":    in.LinkColor,
			"successColor": in.SuccessColor,
			"warningColor": in.WarningColor,
			"errorColor":   in.ErrorColor,
			"sidebarColor": in.SidebarColor,
			"topbarColor":  in.TopbarColor,
			"fontFamily":   in.FontFamily,
		}
		if logo.Replace {
			after["hasLogo"] = len(logo.Logo.Bytes) > 0
		}
		return s.audit.Record(ctx, q, audit.ThemeSettingsUpdated,
			audit.Target{Type: audit.TargetThemeSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, after))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}
