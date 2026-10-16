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
const settingsColumns = `o.id, o.name, s.session_ttl_seconds, s.data_retention_days,
	COALESCE(s.display_name, ''), COALESCE(s.primary_color, ''), COALESCE(s.support_contact, ''), COALESCE(s.privacy_url, ''),
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
	var updatedAt *time.Time
	b := &s.Branding
	err := row.Scan(&s.OrganizationID, &s.OrgName, &ttlSeconds, &retentionDays,
		&b.DisplayName, &b.PrimaryColor, &b.SupportContact, &b.PrivacyURL, &b.HasLogo, &b.HidePoweredBy,
		&updatedAt, &s.RedirectOrigins)
	if ttlSeconds != nil {
		s.Session.SessionTTL = time.Duration(*ttlSeconds) * time.Second
	}
	if retentionDays != nil {
		s.Session.DataRetentionDays = *retentionDays
	}
	if updatedAt != nil {
		s.UpdatedAt = *updatedAt
	}
	return s, err
}

// SaveSession replaces the org's session settings and audits
// identity_proofing.settings_updated with before and after. Saving the settings
// it already has changes and audits nothing.
func (s *OrgSettingsStore) SaveSession(ctx context.Context, orgID uuid.UUID, session SessionSettings) (Settings, error) {
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
		if before.Session == session {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings
				(organization_id, session_ttl_seconds, data_retention_days)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id) DO UPDATE SET
				session_ttl_seconds = EXCLUDED.session_ttl_seconds,
				data_retention_days = EXCLUDED.data_retention_days, updated_at = now()`,
			orgID, int(session.SessionTTL.Seconds()), session.DataRetentionDays); err != nil {
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
				(organization_id, display_name, primary_color, support_contact, privacy_url,
				 logo_bytes, logo_content_type, hide_powered_by)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
				CASE WHEN $6::boolean THEN $7::bytea END, CASE WHEN $6 THEN NULLIF($8::text, '') END, $9)
			ON CONFLICT (organization_id) DO UPDATE SET
				display_name = EXCLUDED.display_name, primary_color = EXCLUDED.primary_color,
				support_contact = EXCLUDED.support_contact, privacy_url = EXCLUDED.privacy_url,
				logo_bytes = CASE WHEN $6 THEN EXCLUDED.logo_bytes ELSE identity_proofing_settings.logo_bytes END,
				logo_content_type = CASE WHEN $6 THEN EXCLUDED.logo_content_type
					ELSE identity_proofing_settings.logo_content_type END,
				hide_powered_by = EXCLUDED.hide_powered_by, updated_at = now()`,
			orgID, b.DisplayName, b.PrimaryColor, b.SupportContact, b.PrivacyURL,
			logo.Replace, nilIfEmpty(logo.Logo.Bytes), logo.Logo.ContentType, b.HidePoweredBy); err != nil {
			return fmt.Errorf("proofing: save branding org %s: %w", orgID, err)
		}
		return s.recordUpdated(ctx, q, orgID, before.Branding.auditFields(), after.auditFields())
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
