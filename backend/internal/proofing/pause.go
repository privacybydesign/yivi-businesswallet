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

// pauseOrgForeignKey is the pause's reference to its organisation: violating
// it is an unknown org.
const pauseOrgForeignKey = "identity_proofing_org_pauses_organization_id_fkey"

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
	// PlatformPausedBy and OrgPausedBy are who set each pause, while it holds:
	// nil when unknown (set before this was kept, or the user is gone).
	PlatformPausedBy *PausedBy
	OrgPausedBy      *PausedBy
}

// PausedBy is the user who set a pause.
type PausedBy struct {
	UserID uuid.UUID
	Name   string
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

// pauseColumns reads a pause over pauseFrom, with who set each level.
const pauseColumns = `p.organization_id, p.platform_paused_at, p.org_paused_at,
	p.platform_paused_by, COALESCE(NULLIF(TRIM(COALESCE(pu.given_names, '') || ' ' || COALESCE(pu.last_name, '')), ''), pu.email, ''),
	p.org_paused_by, COALESCE(NULLIF(TRIM(COALESCE(ou.given_names, '') || ' ' || COALESCE(ou.last_name, '')), ''), ou.email, '')`

const pauseFrom = ` FROM identity_proofing_org_pauses p
	LEFT JOIN users pu ON pu.id = p.platform_paused_by
	LEFT JOIN users ou ON ou.id = p.org_paused_by`

func scanPause(row pgx.Row) (OrgPause, error) {
	var p OrgPause
	var platformBy, orgBy *uuid.UUID
	var platformName, orgName string
	err := row.Scan(&p.OrganizationID, &p.PlatformPausedAt, &p.OrgPausedAt, &platformBy, &platformName, &orgBy, &orgName)
	if platformBy != nil && p.PlatformPausedAt != nil {
		p.PlatformPausedBy = &PausedBy{UserID: *platformBy, Name: platformName}
	}
	if orgBy != nil && p.OrgPausedAt != nil {
		p.OrgPausedBy = &PausedBy{UserID: *orgBy, Name: orgName}
	}
	return p, err
}

// Get returns the org's pause; an org never paused reads as not paused.
func (s *PauseStore) Get(ctx context.Context, orgID uuid.UUID) (OrgPause, error) {
	return getPause(ctx, s.db, orgID, "")
}

func getPause(ctx context.Context, q database.Querier, orgID uuid.UUID, lock string) (OrgPause, error) {
	p, err := scanPause(q.QueryRow(ctx, `SELECT `+pauseColumns+pauseFrom+`
		WHERE p.organization_id = $1`+lock, orgID))
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
	rows, err := s.db.Query(ctx, `SELECT `+pauseColumns+pauseFrom+`
		WHERE p.platform_paused_at IS NOT NULL OR p.org_paused_at IS NOT NULL ORDER BY p.organization_id`)
	if err != nil {
		return nil, fmt.Errorf("proofing: list pauses: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrgPause, error) { return scanPause(row) })
	if err != nil {
		return nil, fmt.Errorf("proofing: list pauses: %w", err)
	}
	return out, nil
}

// pauseUpserts set one level's pause and who set it, keeping when it started
// and by whom while it holds.
var pauseUpserts = map[PauseLevel]string{
	PausePlatform: `INSERT INTO identity_proofing_org_pauses (organization_id, platform_paused_at, platform_paused_by)
		VALUES ($1, CASE WHEN $2 THEN now() END, CASE WHEN $2 THEN $3::uuid END)
		ON CONFLICT (organization_id) DO UPDATE SET
			platform_paused_at = CASE WHEN $2 THEN COALESCE(identity_proofing_org_pauses.platform_paused_at, now()) END,
			platform_paused_by = CASE WHEN $2 THEN CASE WHEN identity_proofing_org_pauses.platform_paused_at IS NULL
				THEN $3::uuid ELSE identity_proofing_org_pauses.platform_paused_by END END,
			updated_at = now()`,
	PauseOrganization: `INSERT INTO identity_proofing_org_pauses (organization_id, org_paused_at, org_paused_by)
		VALUES ($1, CASE WHEN $2 THEN now() END, CASE WHEN $2 THEN $3::uuid END)
		ON CONFLICT (organization_id) DO UPDATE SET
			org_paused_at = CASE WHEN $2 THEN COALESCE(identity_proofing_org_pauses.org_paused_at, now()) END,
			org_paused_by = CASE WHEN $2 THEN CASE WHEN identity_proofing_org_pauses.org_paused_at IS NULL
				THEN $3::uuid ELSE identity_proofing_org_pauses.org_paused_by END END,
			updated_at = now()`,
}

// Set pauses or resumes the org's proofing at level and audits
// identity_proofing.paused or .resumed with "by" the level. Setting what
// already holds changes and audits nothing. An unknown org is ErrOrgNotFound.
func (s *PauseStore) Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, state PauseState, by *uuid.UUID) (OrgPause, error) {
	paused := state == PauseOn
	upsert, ok := pauseUpserts[level]
	if !ok {
		return OrgPause{}, fmt.Errorf("%w: unknown pause level %q", ErrInvalidInput, level)
	}
	var out OrgPause
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getPause(ctx, q, orgID, " FOR UPDATE OF p")
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
		if _, err := q.Exec(ctx, upsert, orgID, paused, by); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == pauseOrgForeignKey {
				return ErrOrgNotFound
			}
			return fmt.Errorf("proofing: set pause org %s: %w", orgID, err)
		}
		if out, err = getPause(ctx, q, orgID, ""); err != nil {
			return err
		}
		action := audit.IdentityProofingResumed
		if paused {
			action = audit.IdentityProofingPaused
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"paused": !paused, "by": string(level)}, map[string]any{"paused": paused, "by": string(level)}))
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
//
// A pause leaves no review open, since a paused org can decide none: every
// open review is rejected with ErrorOrgPaused before the pause is set, and a
// failure there refuses the pause. Those that reach review meanwhile are
// rejected again once it holds; from then on a review is rejected as it
// arrives (rejectIfPaused), and PurgeDue's sweep (rejectPausedReviews) takes
// any whose rejection failed.
func (s *Service) SetProofingPaused(ctx context.Context, orgID uuid.UUID, level PauseLevel, state PauseState, by *uuid.UUID) (OrgPause, error) {
	if s.pauses == nil {
		return OrgPause{}, errors.New("proofing: no pause store configured")
	}
	if state != PauseOn && state != PauseOff {
		return OrgPause{}, fmt.Errorf("%w: unknown pause state %q", ErrInvalidInput, state)
	}
	if state == PauseOn {
		if err := s.rejectOpenReviews(ctx, orgID); err != nil {
			return OrgPause{}, err
		}
	}
	p, err := s.pauses.Set(ctx, orgID, level, state, by)
	if err != nil || state != PauseOn {
		return p, err
	}
	// A review recorded before the pause was set, but after the first pass
	// listed them: one recorded after it rejects itself (rejectIfPaused).
	if err := s.rejectOpenReviews(ctx, orgID); err != nil {
		return OrgPause{}, fmt.Errorf("proofing: paused, but a review is left open for the next sweep: %w", err)
	}
	return p, nil
}

// rejectOpenReviews rejects every review the org has open with
// ErrorOrgPaused. Each is tried; the failures come back joined. One decided
// meanwhile by someone else is no failure.
func (s *Service) rejectOpenReviews(ctx context.Context, orgID uuid.UUID) error {
	open, err := s.requests.ListOpenReviews(ctx, orgID, nil)
	if err != nil {
		return fmt.Errorf("proofing: list open reviews to reject org %s: %w", orgID, err)
	}
	var errs []error
	for _, req := range open {
		if _, err := s.rejectPausedReview(ctx, req); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// rejectPausedReview rejects req's open review with ErrorOrgPaused and
// returns it decided; one decided meanwhile (by a reviewer, or another pass)
// comes back as it was.
func (s *Service) rejectPausedReview(ctx context.Context, req Request) (Request, error) {
	decided, err := s.DecideReview(ctx, req.OrganizationID, req.ID, orgPausedReviewer,
		ReviewInput{Reason: orgPausedReason, ErrorCode: ErrorOrgPaused})
	switch {
	case errors.Is(err, ErrNotUnderReview):
		return req, nil
	case err != nil:
		return req, fmt.Errorf("proofing: reject review request %s of a paused org: %w", req.ID, err)
	}
	return decided, nil
}

// rejectIfPaused rejects req, which just went to review, with ErrorOrgPaused
// when its org is paused: nobody could decide it. The pause is read after the
// review was recorded, and SetProofingPaused lists the open reviews after the
// pause was set, so one of the two always sees the other.
func (s *Service) rejectIfPaused(ctx context.Context, req Request) (Request, error) {
	if s.pauses == nil {
		return req, nil
	}
	p, err := s.pauses.Get(ctx, req.OrganizationID)
	if err != nil || !p.Paused() {
		return req, err
	}
	return s.rejectPausedReview(ctx, req)
}

// rejectPausedReviews rejects every review still open in a paused org: one
// whose rejection failed when the pause was set or when it arrived. Each org
// is tried; the failures come back joined.
func (s *Service) rejectPausedReviews(ctx context.Context) error {
	if s.pauses == nil {
		return nil
	}
	paused, err := s.pauses.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range paused {
		if err := s.rejectOpenReviews(ctx, p.OrganizationID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
