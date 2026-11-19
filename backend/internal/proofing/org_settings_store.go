package proofing

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	COALESCE(s.display_name, ''), COALESCE(s.support_contact, ''), COALESCE(s.privacy_url, ''),
	COALESCE(s.hide_powered_by, false), COALESCE(s.show_theme, true), s.updated_at, COALESCE(s.allowed_redirect_origins, '{}'),
	COALESCE(s.legal_retention_months, 0), COALESCE(s.legal_retention_reason_en, ''), COALESCE(s.legal_retention_reason_nl, '')`

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
	b, legal := &s.Branding, &s.LegalRetention
	err := row.Scan(&s.OrganizationID, &s.OrgName, &ttlSeconds, &retentionDays, &erasureHandling,
		&b.DisplayName, &b.SupportContact, &b.PrivacyURL,
		&b.HidePoweredBy, &b.ShowTheme, &updatedAt, &s.RedirectOrigins, &legal.Months, &legal.ReasonEN, &legal.ReasonNL)
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
	return s.Save(ctx, orgID, change, nil, nil)
}

// SaveRedirectOrigins replaces the origins the org's hosted pages may redirect
// to and be embedded on, and audits identity_proofing.settings_updated with
// before and after. Saving the origins it already has changes nothing.
func (s *OrgSettingsStore) SaveRedirectOrigins(ctx context.Context, orgID uuid.UUID, origins []string) (Settings, error) {
	return s.Save(ctx, orgID, SessionSettingsChange{}, nil, &origins)
}

// Save applies change to the org's session settings and, when legal or
// origins is set, replaces its legal retention or its redirect origins, in one
// transaction: all of it is saved or none of it. It audits
// identity_proofing.settings_updated once, with before and after of what it
// changed; a change to what is already set changes and audits nothing. A new
// legal retention marks the org's engine sessions for a sync
// (engine_retention_stale, SyncEngineRetention).
func (s *OrgSettingsStore) Save(ctx context.Context, orgID uuid.UUID, change SessionSettingsChange, legal *LegalRetention, origins *[]string) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// A request created meanwhile waits on this lock (settingsLockCreate),
		// so it reads the new retention for its purge time.
		if !change.empty() || legal != nil {
			if err := lockSettings(ctx, q, orgID, settingsLockWrite); err != nil {
				return err
			}
		}
		before, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		auditBefore, auditAfter := map[string]any{}, map[string]any{}
		// Applied under the lock, so two changes at once each keep the other's.
		if session := change.apply(before.Session); session != before.Session {
			if err := saveSession(ctx, q, orgID, before.Session, session); err != nil {
				return err
			}
			maps.Copy(auditBefore, before.Session.auditFields())
			maps.Copy(auditAfter, session.auditFields())
		}
		if legal != nil && *legal != before.LegalRetention {
			if err := saveLegalRetention(ctx, q, orgID, *legal); err != nil {
				return err
			}
			maps.Copy(auditBefore, before.LegalRetention.auditFields())
			maps.Copy(auditAfter, legal.auditFields())
		}
		if origins != nil && !slices.Equal(before.RedirectOrigins, *origins) {
			after, err := saveRedirectOrigins(ctx, q, orgID, *origins)
			if err != nil {
				return err
			}
			auditBefore["allowedRedirectOrigins"] = before.RedirectOrigins
			auditAfter["allowedRedirectOrigins"] = after
		}
		if len(auditAfter) == 0 {
			return nil
		}
		return s.recordUpdated(ctx, q, orgID, auditBefore, auditAfter)
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Get(ctx, orgID)
}

// saveRedirectOrigins stores the org's redirect origins, none as an empty
// list, and returns the list it stored.
func saveRedirectOrigins(ctx context.Context, q database.Querier, orgID uuid.UUID, origins []string) ([]string, error) {
	if origins == nil {
		origins = []string{}
	}
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings (organization_id, allowed_redirect_origins)
		VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET allowed_redirect_origins = EXCLUDED.allowed_redirect_origins,
			updated_at = now()`, orgID, origins); err != nil {
		return nil, fmt.Errorf("proofing: save redirect origins org %s: %w", orgID, err)
	}
	return origins, nil
}

// saveSession stores the org's session settings, changed from before to
// session, and moves its requests' purge time when the retention changed. The
// caller holds the settings write lock.
func saveSession(ctx context.Context, q database.Querier, orgID uuid.UUID, before, session SessionSettings) error {
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
	if before.DataRetentionDays != session.DataRetentionDays {
		return refreshOrgPurgeAt(ctx, q, orgID)
	}
	return nil
}

// SaveLegalRetention replaces the org's legal retention (the zero value
// removes it) and audits identity_proofing.settings_updated with before and
// after. A change moves the purge time of the org's requests, which are stored
// again under the same lock as a retention change, and marks the org's engine
// sessions for a sync (engine_retention_stale, SyncEngineRetention). Saving the
// legal retention it has changes, marks and audits nothing.
func (s *OrgSettingsStore) SaveLegalRetention(ctx context.Context, orgID uuid.UUID, legal LegalRetention) (Settings, error) {
	return s.Save(ctx, orgID, SessionSettingsChange{}, &legal, nil)
}

// saveLegalRetention stores the org's legal retention, marks its engine
// sessions for a sync (they hold the old retention until SyncEngineRetention
// gives them the new one) and moves its requests' purge time. The caller holds
// the settings write lock.
func saveLegalRetention(ctx context.Context, q database.Querier, orgID uuid.UUID, legal LegalRetention) error {
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings
			(organization_id, legal_retention_months, legal_retention_reason_en, legal_retention_reason_nl,
			 engine_retention_stale)
		VALUES ($1, NULLIF($2, 0), NULLIF($3, ''), NULLIF($4, ''), true)
		ON CONFLICT (organization_id) DO UPDATE SET
			legal_retention_months = EXCLUDED.legal_retention_months,
			legal_retention_reason_en = EXCLUDED.legal_retention_reason_en,
			legal_retention_reason_nl = EXCLUDED.legal_retention_reason_nl,
			engine_retention_stale = true, updated_at = now()`,
		orgID, legal.Months, legal.ReasonEN, legal.ReasonNL); err != nil {
		return fmt.Errorf("proofing: save legal retention org %s: %w", orgID, err)
	}
	return refreshOrgPurgeAt(ctx, q, orgID)
}

// SyncEngineRetention runs sync with the org's settings as they stand under its
// settings write lock (settingsLockWrite), which every settings save takes: no
// save or other sync runs between that read and sync's engine writes, and a
// request insert waits for it. Once sync succeeded it clears the org's
// engine_retention_stale; a failed sync leaves it set for the pruner's next run
// (StaleEngineRetention).
func (s *OrgSettingsStore) SyncEngineRetention(ctx context.Context, orgID uuid.UUID, sync func(context.Context, Settings) error) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if err := lockSettings(ctx, q, orgID, settingsLockWrite); err != nil {
			return err
		}
		settings, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		if err := sync(ctx, settings); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_settings SET engine_retention_stale = false
			WHERE organization_id = $1 AND engine_retention_stale`, orgID); err != nil {
			return fmt.Errorf("proofing: clear stale engine retention org %s: %w", orgID, err)
		}
		return nil
	})
}

// MarkEngineRetentionStale marks the org's engine sessions for the next sync
// (SyncEngineRetention). An org without settings has nothing a sync would
// change: its sessions hold the defaults they were created with.
func (s *OrgSettingsStore) MarkEngineRetentionStale(ctx context.Context, orgID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_settings SET engine_retention_stale = true
		WHERE organization_id = $1`, orgID); err != nil {
		return fmt.Errorf("proofing: mark stale engine retention org %s: %w", orgID, err)
	}
	return nil
}

// StaleEngineRetention lists the orgs whose engine sessions may not hold the
// retention their settings give them yet (engine_retention_stale).
func (s *OrgSettingsStore) StaleEngineRetention(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT organization_id FROM identity_proofing_settings
		WHERE engine_retention_stale ORDER BY organization_id`)
	if err != nil {
		return nil, fmt.Errorf("proofing: list stale engine retention: %w", err)
	}
	orgs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("proofing: list stale engine retention: %w", err)
	}
	return orgs, nil
}

// SaveBranding replaces what the org's branding says besides its theme, and
// audits identity_proofing.settings_updated with before and after.
func (s *OrgSettingsStore) SaveBranding(ctx context.Context, orgID uuid.UUID, b Branding) (Settings, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getSettings(ctx, q, orgID)
		if err != nil {
			return err
		}
		if equalBranding(b, before.Branding) {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_settings
				(organization_id, display_name, support_contact, privacy_url, hide_powered_by, show_theme)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, $6)
			ON CONFLICT (organization_id) DO UPDATE SET
				display_name = EXCLUDED.display_name, support_contact = EXCLUDED.support_contact,
				privacy_url = EXCLUDED.privacy_url, hide_powered_by = EXCLUDED.hide_powered_by,
				show_theme = EXCLUDED.show_theme, updated_at = now()`,
			orgID, b.DisplayName, b.SupportContact, b.PrivacyURL, b.HidePoweredBy, b.ShowTheme); err != nil {
			return fmt.Errorf("proofing: save branding org %s: %w", orgID, err)
		}
		return s.recordUpdated(ctx, q, orgID, before.Branding.auditFields(), b.auditFields())
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Get(ctx, orgID)
}

// equalBranding compares what an org's branding saves.
func equalBranding(a, b Branding) bool {
	return a.DisplayName == b.DisplayName && a.SupportContact == b.SupportContact &&
		a.PrivacyURL == b.PrivacyURL && a.HidePoweredBy == b.HidePoweredBy && a.ShowTheme == b.ShowTheme
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
