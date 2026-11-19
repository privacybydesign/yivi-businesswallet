package organization

import (
	"testing"
	"time"
)

func TestTallyMemberInsights(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	past := now.AddDate(0, -1, 0)
	soon := now.AddDate(0, 0, 10)
	later := now.AddDate(1, 0, 0)
	valid := "valid"
	rejected := "rejected"

	snaps := []MemberStatusSnapshot{
		// Never identified, no VOG on file.
		{MemberType: MemberTypeEmployee},
		// Identified and current; VOG valid for a year and covering the requirement.
		{MemberType: MemberTypeEmployee, IdentityVerifiedAt: &past, IdentityDueAt: &later, VogLastResult: &valid, VogValidUntil: &later, VogCoveredCodes: []string{"11"}},
		// Identification due soon; VOG passed but no longer covers today's requirement.
		{MemberType: MemberTypeEmployee, IdentityVerifiedAt: &past, IdentityDueAt: &soon, VogLastResult: &valid, VogValidUntil: &later},
		// Identification overdue; latest VOG attempt rejected.
		{MemberType: MemberTypeEmployee, IdentityVerifiedAt: &past, IdentityDueAt: &past, VogLastResult: &rejected},
		// Admin asked for both.
		{MemberType: MemberTypeEmployee, IdentityRequestedAt: &past, VogRequestedAt: &past},
		// An external: identified, and the policy does not require a VOG of externals.
		{MemberType: MemberTypeExternal, IdentityVerifiedAt: &past},
	}
	settings := ScreeningSettings{RequiredFor: ScreeningRequiredForEmployees, RequiredCodes: []string{"11"}}

	got := TallyMemberInsights(snaps, 30, settings, now)

	if got.Members != 6 {
		t.Errorf("Members = %d, want 6", got.Members)
	}
	wantIdentity := map[string]int{
		IdentityStatusNever: 1, IdentityStatusVerified: 2, IdentityStatusDueSoon: 1,
		IdentityStatusOverdue: 1, IdentityStatusRequested: 1,
	}
	for status, want := range wantIdentity {
		if got.Identity[status] != want {
			t.Errorf("Identity[%s] = %d, want %d", status, got.Identity[status], want)
		}
	}
	wantScreening := map[string]int{
		ScreeningStatusNotRequired: 1, ScreeningStatusNone: 1, ScreeningStatusRequested: 1,
		ScreeningStatusValid: 1, ScreeningStatusExpiring: 0, ScreeningStatusExpired: 0,
		ScreeningStatusRejected: 1, ScreeningStatusRecheckRequired: 1,
	}
	for status, want := range wantScreening {
		count, present := got.Screening[status]
		if !present {
			t.Errorf("Screening lacks %s (every status must be reported, 0 included)", status)
		}
		if count != want {
			t.Errorf("Screening[%s] = %d, want %d", status, count, want)
		}
	}
}

func TestTallyMemberInsightsEmptyOrg(t *testing.T) {
	got := TallyMemberInsights(nil, 30, ScreeningSettings{RequiredFor: ScreeningRequiredForNobody}, time.Now())
	if got.Members != 0 || len(got.Identity) != len(identityStatuses) || len(got.Screening) != len(screeningStatuses) {
		t.Errorf("empty tally = %+v, want zero members with every status reported", got)
	}
}
