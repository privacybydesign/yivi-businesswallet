package organization

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// MemberStatusSnapshot is the raw identity and screening bookkeeping of one
// active membership - the same columns the member list decorates - as the
// input to an org-wide tally of derived statuses (TallyMemberInsights).
type MemberStatusSnapshot struct {
	MemberType          string
	IdentityVerifiedAt  *time.Time
	IdentityDueAt       *time.Time
	IdentityRequestedAt *time.Time
	VogLastResult       *string
	VogValidUntil       *time.Time
	VogCoveredCodes     []string
	VogRequestedAt      *time.Time
}

// MemberInsights is the admin's overview of an org's members by derived
// identity and screening status: how many have never identified, are overdue,
// hold no valid VOG, and so on. It counts active memberships only - a pending
// invitation has no identity or screening to speak of - and is computed on
// read from the same pure derivations the member list uses
// (DeriveIdentityStatus, DeriveScreeningStatus), so the two can never disagree.
// Every known status is present, with 0 when no member is in it.
type MemberInsights struct {
	Members   int            `json:"members"`
	Identity  map[string]int `json:"identity"`
	Screening map[string]int `json:"screening"`
}

// identityStatuses / screeningStatuses are every status the derivations can
// produce, so a tally reports each one even when empty.
var (
	identityStatuses = []string{
		IdentityStatusNever, IdentityStatusVerified, IdentityStatusDueSoon,
		IdentityStatusOverdue, IdentityStatusRequested,
	}
	screeningStatuses = []string{
		ScreeningStatusNotRequired, ScreeningStatusNone, ScreeningStatusRequested,
		ScreeningStatusValid, ScreeningStatusExpiring, ScreeningStatusExpired,
		ScreeningStatusRejected, ScreeningStatusRecheckRequired,
	}
)

// TallyMemberInsights derives every member's identity and screening status
// under the org's current policy and counts them. It is server-side on purpose:
// the member list is paged (MaxMemberListLimit), so a client-side tally would
// silently undercount any org past one page - the "count that disagrees with
// the page" failure .ai/features/member-reidentification.md §11 warns about.
func TallyMemberInsights(snaps []MemberStatusSnapshot, identityLookaheadDays int, screening ScreeningSettings, now time.Time) MemberInsights {
	out := MemberInsights{
		Members:   len(snaps),
		Identity:  make(map[string]int, len(identityStatuses)),
		Screening: make(map[string]int, len(screeningStatuses)),
	}
	for _, s := range identityStatuses {
		out.Identity[s] = 0
	}
	for _, s := range screeningStatuses {
		out.Screening[s] = 0
	}
	for _, m := range snaps {
		out.Identity[DeriveIdentityStatus(m.IdentityVerifiedAt, m.IdentityDueAt, m.IdentityRequestedAt, now, identityLookaheadDays)]++
		out.Screening[DeriveScreeningStatus(
			screening.RequiredForMember(m.MemberType),
			screeningSnapshot{lastResult: m.VogLastResult, validUntil: m.VogValidUntil, coveredCodes: m.VogCoveredCodes, requestedAt: m.VogRequestedAt},
			screening.RequiredCodes, now, screening.LookaheadDays())]++
	}
	return out
}

// MemberStatusSnapshots reads every active membership's identity and screening
// columns for orgID - the whole org, unpaged, which is what an honest count
// needs and what the paged member list cannot give.
func (s *Store) MemberStatusSnapshots(ctx context.Context, orgID uuid.UUID) ([]MemberStatusSnapshot, error) {
	const q = `
		SELECT member_type, identity_verified_at, identity_due_at, identity_requested_at,
		       vog_last_result, vog_valid_until, vog_covered_codes, vog_requested_at
		FROM memberships
		WHERE organization_id = $1`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: member status snapshots org %s: %w", orgID, err)
	}
	defer rows.Close()

	var snaps []MemberStatusSnapshot
	for rows.Next() {
		var m MemberStatusSnapshot
		if err := rows.Scan(&m.MemberType, &m.IdentityVerifiedAt, &m.IdentityDueAt, &m.IdentityRequestedAt,
			&m.VogLastResult, &m.VogValidUntil, &m.VogCoveredCodes, &m.VogRequestedAt); err != nil {
			return nil, fmt.Errorf("organization: member status snapshots scan: %w", err)
		}
		snaps = append(snaps, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: member status snapshots rows: %w", err)
	}
	return snaps, nil
}

// memberInsights is GET /orgs/{slug}/member-insights: the org dashboard's
// admin overview of members by identity and screening status.
func (h *Handler) memberInsights(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	org := OrgFromContext(ctx)

	snaps, err := h.store.MemberStatusSnapshots(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("reading member status snapshots: %w", err)
	}
	lookahead, err := h.identityLookaheadDays(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("resolving identity lookahead: %w", err)
	}
	screening, err := h.store.GetScreeningSettings(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("resolving screening settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, TallyMemberInsights(snaps, lookahead, screening, time.Now()))
	return nil
}
