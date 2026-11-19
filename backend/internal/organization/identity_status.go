package organization

import "time"

// DeriveIdentityStatus computes a membership's re-identification status from its
// three timestamps and the org's reminder lookahead window (#240 §1). It is a
// pure function — the status is never stored, only ever recomputed from
// identity_verified_at / identity_due_at / identity_requested_at — so a change to
// the org's policy or the passage of time is reflected the moment it is read,
// with nothing to keep in sync.
//
// Precedence: requested (an admin explicitly asked) outranks overdue/due-soon,
// because it is the more actionable fact for the member to see; overdue outranks
// due-soon; a membership with no due date at all (no policy applies, or never
// verified) falls back to verified/never.
func DeriveIdentityStatus(verifiedAt, dueAt, requestedAt *time.Time, now time.Time, lookaheadDays int) string {
	if requestedAt != nil {
		return IdentityStatusRequested
	}
	if dueAt != nil {
		switch {
		case !now.Before(*dueAt):
			return IdentityStatusOverdue
		case !now.Add(time.Duration(lookaheadDays) * 24 * time.Hour).Before(*dueAt):
			return IdentityStatusDueSoon
		}
	}
	if verifiedAt != nil {
		return IdentityStatusVerified
	}
	return IdentityStatusNever
}

// withIdentityStatus decorates a Member with its derived IdentityStatus.
func (m Member) withIdentityStatus(now time.Time, lookaheadDays int) Member {
	m.IdentityStatus = DeriveIdentityStatus(m.IdentityVerifiedAt, m.IdentityDueAt, m.IdentityRequestedAt, now, lookaheadDays)
	return m
}

// withIdentityStatus decorates a MemberEntry with its derived IdentityStatus. An
// invited entry has no membership row and is always "never" (DeriveIdentityStatus
// agrees: all three timestamps are nil on that branch of the union).
func (e MemberEntry) withIdentityStatus(now time.Time, lookaheadDays int) MemberEntry {
	e.IdentityStatus = DeriveIdentityStatus(e.IdentityVerifiedAt, e.IdentityDueAt, e.IdentityRequestedAt, now, lookaheadDays)
	return e
}
