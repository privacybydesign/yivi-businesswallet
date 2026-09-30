package proofing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

var (
	// ErrProofingPaused is any proofing call for an org whose proofing is
	// paused, by a platform admin or by the org's admin.
	ErrProofingPaused = errors.New("proofing: identity proofing is paused for this organisation")
	// ErrOrgNotFound is a pause for an org that does not exist.
	ErrOrgNotFound = errors.New("proofing: organisation not found")
)

// foreignKeyViolation is Postgres' SQLSTATE for a foreign key violation.
const foreignKeyViolation = "23503"

// PauseLevel is who paused an org's proofing.
type PauseLevel string

const (
	// PausePlatform is a platform admin's pause: the org's admin cannot lift it.
	PausePlatform PauseLevel = "platform"
	// PauseOrganization is the org's own admin switching proofing off.
	PauseOrganization PauseLevel = "organization"
)

// OrgPause is whether an org's proofing is paused, and by whom. The zero value
// (apart from the id) is an org proofing as usual.
type OrgPause struct {
	OrganizationID   uuid.UUID
	PlatformPausedAt *time.Time
	OrgPausedAt      *time.Time
}

// Paused reports whether either pause stops the org's proofing.
func (p OrgPause) Paused() bool { return p.PlatformPausedAt != nil || p.OrgPausedAt != nil }

// PauseStore persists the pauses; every change is audited in its transaction.
type PauseStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewPauseStore(db database.DB, recorder audit.Recorder) *PauseStore {
	return &PauseStore{db: db, audit: recorder}
}

const pauseColumns = `organization_id, platform_paused_at, org_paused_at`

func scanPause(row pgx.Row) (OrgPause, error) {
	var p OrgPause
	err := row.Scan(&p.OrganizationID, &p.PlatformPausedAt, &p.OrgPausedAt)
	return p, err
}

// Get returns the org's pause; an org never paused reads as not paused.
func (s *PauseStore) Get(ctx context.Context, orgID uuid.UUID) (OrgPause, error) {
	return getPause(ctx, s.db, orgID, "")
}

func getPause(ctx context.Context, q database.Querier, orgID uuid.UUID, lock string) (OrgPause, error) {
	p, err := scanPause(q.QueryRow(ctx, `SELECT `+pauseColumns+` FROM identity_proofing_org_pauses
		WHERE organization_id = $1`+lock, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgPause{OrganizationID: orgID}, nil
	}
	if err != nil {
		return OrgPause{}, fmt.Errorf("proofing: read pause org %s: %w", orgID, err)
	}
	return p, nil
}

// List returns every org whose proofing is paused.
func (s *PauseStore) List(ctx context.Context) ([]OrgPause, error) {
	rows, err := s.db.Query(ctx, `SELECT `+pauseColumns+` FROM identity_proofing_org_pauses
		WHERE platform_paused_at IS NOT NULL OR org_paused_at IS NOT NULL ORDER BY organization_id`)
	if err != nil {
		return nil, fmt.Errorf("proofing: list pauses: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrgPause, error) { return scanPause(row) })
	if err != nil {
		return nil, fmt.Errorf("proofing: list pauses: %w", err)
	}
	return out, nil
}

// pauseUpserts set one level's pause, keeping when it started while it holds.
var pauseUpserts = map[PauseLevel]string{
	PausePlatform: `INSERT INTO identity_proofing_org_pauses (organization_id, platform_paused_at)
		VALUES ($1, CASE WHEN $2 THEN now() END)
		ON CONFLICT (organization_id) DO UPDATE SET
			platform_paused_at = CASE WHEN $2 THEN COALESCE(identity_proofing_org_pauses.platform_paused_at, now()) END,
			updated_at = now()
		RETURNING ` + pauseColumns,
	PauseOrganization: `INSERT INTO identity_proofing_org_pauses (organization_id, org_paused_at)
		VALUES ($1, CASE WHEN $2 THEN now() END)
		ON CONFLICT (organization_id) DO UPDATE SET
			org_paused_at = CASE WHEN $2 THEN COALESCE(identity_proofing_org_pauses.org_paused_at, now()) END,
			updated_at = now()
		RETURNING ` + pauseColumns,
}

// Set pauses or resumes the org's proofing at level and audits
// identity_proofing.paused or .resumed with "by" the level. Setting what
// already holds changes and audits nothing. An unknown org is ErrOrgNotFound.
func (s *PauseStore) Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, paused bool) (OrgPause, error) {
	upsert, ok := pauseUpserts[level]
	if !ok {
		return OrgPause{}, fmt.Errorf("%w: unknown pause level %q", ErrInvalidInput, level)
	}
	var out OrgPause
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getPause(ctx, q, orgID, " FOR UPDATE")
		if err != nil {
			return err
		}
		held := before.PlatformPausedAt != nil
		if level == PauseOrganization {
			held = before.OrgPausedAt != nil
		}
		if held == paused {
			out = before
			return nil
		}
		if out, err = scanPause(q.QueryRow(ctx, upsert, orgID, paused)); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
				return ErrOrgNotFound
			}
			return fmt.Errorf("proofing: set pause org %s: %w", orgID, err)
		}
		action := audit.IdentityProofingResumed
		if paused {
			action = audit.IdentityProofingPaused
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			map[string]any{"by": string(level)})
	})
	return out, err
}

// checkActive refuses proofing for an org that is paused. A service without a
// pause store never pauses.
func (s *Service) checkActive(ctx context.Context, orgID uuid.UUID) error {
	if s.pauses == nil {
		return nil
	}
	p, err := s.pauses.Get(ctx, orgID)
	if err != nil {
		return err
	}
	if p.Paused() {
		return ErrProofingPaused
	}
	return nil
}

// ProofingPause returns whether the org's proofing is paused, and by whom.
func (s *Service) ProofingPause(ctx context.Context, orgID uuid.UUID) (OrgPause, error) {
	if s.pauses == nil {
		return OrgPause{OrganizationID: orgID}, nil
	}
	return s.pauses.Get(ctx, orgID)
}

// ProofingPauses lists every paused org, for the platform admin.
func (s *Service) ProofingPauses(ctx context.Context) ([]OrgPause, error) {
	if s.pauses == nil {
		return []OrgPause{}, nil
	}
	return s.pauses.List(ctx)
}

// SetProofingPaused pauses or resumes the org's proofing at level. Sessions
// already running still settle and their webhooks still go out; nothing new
// starts while either level holds.
func (s *Service) SetProofingPaused(ctx context.Context, orgID uuid.UUID, level PauseLevel, paused bool) (OrgPause, error) {
	if s.pauses == nil {
		return OrgPause{}, errors.New("proofing: no pause store configured")
	}
	return s.pauses.Set(ctx, orgID, level, paused)
}
