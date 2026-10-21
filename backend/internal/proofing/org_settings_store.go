package proofing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// OrgSettingsStore persists what an org sets once for all its flows: session
// lifetime and retention, branding and redirect origins. An org that saved
// nothing reads as the defaults. Every mutation is audited in its own
// transaction.
type OrgSettingsStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewOrgSettingsStore(db database.DB, recorder audit.Recorder) *OrgSettingsStore {
	return &OrgSettingsStore{db: db, audit: recorder}
}

// settingsColumns reads an org's settings over settingsFrom; the columns of an
// org that saved none are NULL, which scanSettings reads as the defaults.
const settingsColumns = `o.id, o.name, s.session_ttl_seconds, s.data_retention_days, s.erasure_handling,
	COALESCE(s.display_name, ''), COALESCE(s.primary_color, ''), COALESCE(s.secondary_color, ''),
	COALESCE(s.support_contact, ''), COALESCE(s.privacy_url, ''),
	COALESCE(s.text_color, ''), COALESCE(s.surface_color, ''), COALESCE(s.border_color, ''), COALESCE(s.link_color, ''),
	COALESCE(s.success_color, ''), COALESCE(s.warning_color, ''), COALESCE(s.error_color, ''), COALESCE(s.topbar_color, ''),
	COALESCE(s.font_family, ''),
	s.logo_bytes IS NOT NULL, COALESCE(s.hide_powered_by, false), s.updated_at, COALESCE(s.allowed_redirect_origins, '{}')`

const settingsFrom = ` FROM organizations o
	LEFT JOIN identity_proofing_settings s ON s.organization_id = o.id`

// Get returns the org's settings, or the defaults when it saved none.
func (s *OrgSettingsStore) Get(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	return getSettings(ctx, s.db, orgID)
}

// settingsLockKey namespaces lockSettings' advisory lock.
const settingsLockKey = "identity_proofing_settings:"

// settingsLock is how strongly lockSettings holds an org's settings.
type settingsLock string

const (
	// settingsLockWrite is a settings write's: it waits for every request
	// insert in flight and holds the next ones off until it commits.
	settingsLockWrite settingsLock = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
	// settingsLockCreate is a request insert's: it waits on a settings write in
	// flight, and holds the next one off until it commits. Inserts share it.
	settingsLockCreate settingsLock = `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`
)

// lockSettings holds an org's settings as strength says for the rest of q's
// transaction. It is an advisory lock on the org, not on the settings row,
// since an org that saved nothing has none.
//
// Lock order: a request insert takes settingsLockCreate before any request row,
// so none is created against a retention being changed (Create). A retention
// write takes settingsLockWrite, then locks the org's requests in id order
// (refreshOrgPurgeAt), as every statement that waits on several requests' rows
// must (ClearExpiredProofedNames). No other writer takes the lock, so none
// waits on it while holding a request's row.
func lockSettings(ctx context.Context, q database.Querier, orgID uuid.UUID, strength settingsLock) error {
	if _, err := q.Exec(ctx, string(strength), settingsLockKey+orgID.String()); err != nil {
		return fmt.Errorf("proofing: lock settings org %s: %w", orgID, err)
	}
	return nil
}

func getSettings(ctx context.Context, q database.Querier, orgID uuid.UUID) (Settings, error) {
	rows, err := q.Query(ctx, `SELECT `+settingsColumns+settingsFrom+` WHERE o.id = $1`, orgID)
	if err != nil {
		return Settings{}, fmt.Errorf("proofing: read settings org %s: %w", orgID, err)
	}
	settings, err := pgx.CollectExactlyOneRow(rows, scanSettings)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, ErrOrgNotFound
	}
	if err != nil {
		return Settings{}, fmt.Errorf("proofing: read settings org %s: %w", orgID, err)
	}
	return settings, nil
}

// scanSettings reads a row of settingsColumns: an org that saved none gets the
// defaults.
func scanSettings(row pgx.CollectableRow) (Settings, error) {
	s := defaultSettings(uuid.Nil)
	var ttlSeconds, retentionDays *int
	var erasureHandling *ErasureHandling
	var updatedAt *time.Time
	b := &s.Branding
	err := row.Scan(&s.OrganizationID, &s.OrgName, &ttlSeconds, &retentionDays, &erasureHandling,
		&b.DisplayName, &b.PrimaryColor, &b.SecondaryColor, &b.SupportContact, &b.PrivacyURL,
		&b.TextColor, &b.SurfaceColor, &b.BorderColor, &b.LinkColor, &b.SuccessColor, &b.WarningColor, &b.ErrorColor,
		&b.TopbarColor, &b.FontFamily, &b.HasLogo, &b.HidePoweredBy, &updatedAt, &s.RedirectOrigins)
	if ttlSeconds != nil {
		s.Session.SessionTTL = time.Duration(*ttlSeconds) * time.Second
	}
	if retentionDays != nil {
		s.Session.DataRetentionDays = *retentionDays
	}
	if erasureHandling != nil {
		s.Session.ErasureHandling = *erasureHandling
	}
	if updatedAt != nil {
		s.UpdatedAt = *updatedAt
	}
	return s, err
}

// SaveSession applies change to the org's session settings and audits
// identity_proofing.settings_updated with before and after. A change to what
// is already set changes and audits nothing.
func (s *OrgSettingsStore) SaveSession(ctx context.Context, orgID uuid.UUID, change SessionSettingsChange) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// A request created meanwhile waits on this lock (settingsLockCreate),
		// so it reads the new retention for its purge time.
		if err := lockSettings(ctx, q, orgID, settingsLockWrite); err != nil {
			return err
		}
		before, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		// Applied under the lock, so two changes at once each keep the other's.
		session := change.apply(before.Session)
		if before.Session == session {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings
				(organization_id, session_ttl_seconds, data_retention_days, erasure_handling)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (organization_id) DO UPDATE SET
				session_ttl_seconds = EXCLUDED.session_ttl_seconds,
				data_retention_days = EXCLUDED.data_retention_days,
				erasure_handling = EXCLUDED.erasure_handling, updated_at = now()`,
			orgID, int(session.SessionTTL.Seconds()), session.DataRetentionDays, string(session.ErasureHandling)); err != nil {
			return fmt.Errorf("proofing: save session settings org %s: %w", orgID, err)
		}
		if before.Session.DataRetentionDays != session.DataRetentionDays {
			if err := refreshOrgPurgeAt(ctx, q, orgID); err != nil {
				return err
			}
		}
		return s.recordUpdated(ctx, q, orgID, before.Session.auditFields(), session.auditFields())
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Get(ctx, orgID)
}

// SaveRedirectOrigins replaces the origins the org's hosted pages may redirect
// to and be embedded on, and audits identity_proofing.settings_updated with
// before and after. Saving the origins it already has changes nothing.
func (s *OrgSettingsStore) SaveRedirectOrigins(ctx context.Context, orgID uuid.UUID, origins []string) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		if slices.Equal(before.RedirectOrigins, origins) {
			return nil
		}
		if origins == nil {
			origins = []string{}
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings (organization_id, allowed_redirect_origins)
			VALUES ($1, $2)
			ON CONFLICT (organization_id) DO UPDATE SET allowed_redirect_origins = EXCLUDED.allowed_redirect_origins,
				updated_at = now()`, orgID, origins); err != nil {
			return fmt.Errorf("proofing: save redirect origins org %s: %w", orgID, err)
		}
		return s.recordUpdated(ctx, q, orgID,
			map[string]any{"allowedRedirectOrigins": before.RedirectOrigins},
			map[string]any{"allowedRedirectOrigins": origins})
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Get(ctx, orgID)
}

// SaveBranding replaces the org's branding, and its logo as logo says, and
// audits identity_proofing.settings_updated with before and after (whether a
// logo is set, never its bytes).
func (s *OrgSettingsStore) SaveBranding(ctx context.Context, orgID uuid.UUID, b Branding, logo LogoChange) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		after := b
		after.HasLogo = before.Branding.HasLogo
		if logo.Replace {
			after.HasLogo = len(logo.Logo.Bytes) > 0
		}
		// A new logo is a change even when one was set before: only its bytes
		// differ, which the branding does not hold.
		replaced := logo.Replace && len(logo.Logo.Bytes) > 0
		if after == before.Branding && !replaced {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings
				(organization_id, display_name, primary_color, secondary_color, support_contact, privacy_url,
				 logo_bytes, logo_content_type, hide_powered_by,
				 text_color, surface_color, border_color, link_color, success_color, warning_color, error_color,
				 topbar_color, font_family)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
				CASE WHEN $7::boolean THEN $8::bytea END, CASE WHEN $7 THEN NULLIF($9::text, '') END, $10,
				NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''), NULLIF($16, ''),
				NULLIF($17, ''), NULLIF($18, ''), NULLIF($19, ''))
			ON CONFLICT (organization_id) DO UPDATE SET
				display_name = EXCLUDED.display_name, primary_color = EXCLUDED.primary_color,
				secondary_color = EXCLUDED.secondary_color,
				support_contact = EXCLUDED.support_contact, privacy_url = EXCLUDED.privacy_url,
				logo_bytes = CASE WHEN $7 THEN EXCLUDED.logo_bytes ELSE identity_proofing_settings.logo_bytes END,
				logo_content_type = CASE WHEN $7 THEN EXCLUDED.logo_content_type
					ELSE identity_proofing_settings.logo_content_type END,
				hide_powered_by = EXCLUDED.hide_powered_by,
				text_color = EXCLUDED.text_color, surface_color = EXCLUDED.surface_color,
				border_color = EXCLUDED.border_color, link_color = EXCLUDED.link_color,
				success_color = EXCLUDED.success_color, warning_color = EXCLUDED.warning_color,
				error_color = EXCLUDED.error_color, topbar_color = EXCLUDED.topbar_color,
				font_family = EXCLUDED.font_family, updated_at = now()`,
			orgID, b.DisplayName, b.PrimaryColor, b.SecondaryColor, b.SupportContact, b.PrivacyURL,
			logo.Replace, nilIfEmpty(logo.Logo.Bytes), logo.Logo.ContentType, b.HidePoweredBy,
			b.TextColor, b.SurfaceColor, b.BorderColor, b.LinkColor, b.SuccessColor, b.WarningColor, b.ErrorColor,
			b.TopbarColor, b.FontFamily); err != nil {
			return fmt.Errorf("proofing: save branding org %s: %w", orgID, err)
		}
		changed := after.auditFields()
		if replaced && before.Branding.HasLogo {
			changed["logoReplaced"] = true
		}
		return s.recordUpdated(ctx, q, orgID, before.Branding.auditFields(), changed)
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Get(ctx, orgID)
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *OrgSettingsStore) recordUpdated(ctx context.Context, q database.Querier, orgID uuid.UUID, before, after map[string]any) error {
	return s.audit.Record(ctx, q, audit.IdentityProofingSettingsUpdated,
		audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
		audit.Updated(before, after))
}

// Logo returns the org's logo, or ErrNoLogo.
func (s *OrgSettingsStore) Logo(ctx context.Context, orgID uuid.UUID) (Logo, error) {
	var logo Logo
	var contentType *string
	err := s.db.QueryRow(ctx, `SELECT logo_bytes, logo_content_type FROM identity_proofing_settings
		WHERE organization_id = $1`, orgID).Scan(&logo.Bytes, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Logo{}, ErrNoLogo
	}
	if err != nil {
		return Logo{}, fmt.Errorf("proofing: read logo org %s: %w", orgID, err)
	}
	if contentType == nil {
		return Logo{}, ErrNoLogo
	}
	logo.ContentType = *contentType
	return logo, nil
}
