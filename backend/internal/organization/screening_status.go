package organization

import (
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

// screeningSnapshot is the subset of a membership's screening bookkeeping
// DeriveScreeningStatus needs, so it can be called the same way from a Member,
// a MemberEntry or a bare set of columns without depending on either type.
type screeningSnapshot struct {
	lastResult   *string
	validUntil   *time.Time
	coveredCodes []string
	requestedAt  *time.Time
}

// DeriveScreeningStatus computes a membership's VOG status from the org's
// requirement and the member's latest screening attempt (#242 §1). Like
// DeriveIdentityStatus it is a pure function - the status is never stored, only
// ever recomputed from vog_last_result / vog_valid_until / vog_covered_codes /
// vog_requested_at plus the org's current required codes - so a policy change
// or the passage of time is reflected the moment it is read.
//
// Precedence, most to least specific:
//   - not_required: the org's policy does not require a VOG for this member's
//     type at all: everything else is moot.
//   - requested: an admin explicitly asked, the same "outranks the rest" rule
//     DeriveIdentityStatus uses, because it is the most actionable fact for the
//     member to see.
//   - none: required, but no screening attempt is on file yet.
//   - rejected: the *latest* attempt did not pass (rejected, mismatch or
//     insufficient scope) - deliberately independent of whether an older,
//     still-unexpired valid record exists: the most recent evidence about a
//     person is what a screening decision should reflect, not a stale pass a
//     subsequent failed attempt has since called into question.
//   - recheck_required: the latest attempt passed, but does not cover every
//     code the org currently requires - typically because the requirement grew
//     after the check ran, and the stored record only ever proves the subset it
//     was evaluated against at the time (#242 §1).
//   - expired / expiring / valid: ValidUntil against now and the lookahead
//     window, the same shape as DeriveIdentityStatus's overdue/due_soon/verified
//     split.
func DeriveScreeningStatus(requiredForMember bool, snap screeningSnapshot, requiredCodes []string, now time.Time, lookaheadDays int) string {
	if !requiredForMember {
		return ScreeningStatusNotRequired
	}
	if snap.requestedAt != nil {
		return ScreeningStatusRequested
	}
	if snap.lastResult == nil {
		return ScreeningStatusNone
	}
	if *snap.lastResult != string(vog.ResultValid) {
		return ScreeningStatusRejected
	}
	if !coversAll(requiredCodes, snap.coveredCodes) {
		return ScreeningStatusRecheckRequired
	}
	if snap.validUntil != nil {
		switch {
		case !now.Before(*snap.validUntil):
			return ScreeningStatusExpired
		case !now.Add(time.Duration(lookaheadDays) * 24 * time.Hour).Before(*snap.validUntil):
			return ScreeningStatusExpiring
		}
	}
	return ScreeningStatusValid
}

// coversAll reports whether every code in required is present in covered - the
// check behind ScreeningStatusRecheckRequired. An empty requirement is always
// covered, including by a record with no covered codes at all (a screening
// method - the credential path - that never carried codes because the org
// required none).
func coversAll(required, covered []string) bool {
	for _, code := range required {
		if !slices.Contains(covered, code) {
			return false
		}
	}
	return true
}

// withVogStatus decorates a Member with its derived VogStatus.
func (m Member) withVogStatus(settings ScreeningSettings, now time.Time) Member {
	m.VogStatus = DeriveScreeningStatus(
		settings.RequiredForMember(m.MemberType),
		screeningSnapshot{lastResult: m.VogLastResult, validUntil: m.VogValidUntil, coveredCodes: m.VogCoveredCodes, requestedAt: m.VogRequestedAt},
		settings.RequiredCodes, now, settings.LookaheadDays())
	return m
}

// withVogStatus decorates a MemberEntry with its derived VogStatus. An invited
// entry has no membership row and is always "not_required" or "none" depending
// on the policy (DeriveScreeningStatus agrees: every screening field is nil on
// that branch of the union).
func (e MemberEntry) withVogStatus(settings ScreeningSettings, now time.Time) MemberEntry {
	e.VogStatus = DeriveScreeningStatus(
		settings.RequiredForMember(e.MemberType),
		screeningSnapshot{lastResult: e.VogLastResult, validUntil: e.VogValidUntil, coveredCodes: e.VogCoveredCodes, requestedAt: e.VogRequestedAt},
		settings.RequiredCodes, now, settings.LookaheadDays())
	return e
}
