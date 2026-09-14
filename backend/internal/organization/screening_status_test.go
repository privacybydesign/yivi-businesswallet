package organization

import (
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

func TestDeriveScreeningStatus(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	t1 := func(delta time.Duration) *time.Time {
		v := now.Add(delta)
		return &v
	}
	result := func(r vog.Result) *string {
		s := string(r)
		return &s
	}

	tests := []struct {
		name      string
		required  bool
		snap      screeningSnapshot
		codes     []string
		lookahead int
		want      string
	}{
		{"not required for this member type", false, screeningSnapshot{}, nil, 30, ScreeningStatusNotRequired},
		{"required, never checked", true, screeningSnapshot{}, nil, 30, ScreeningStatusNone},
		{
			"requested wins over never checked", true,
			screeningSnapshot{requestedAt: t1(-time.Hour)},
			nil, 30, ScreeningStatusRequested,
		},
		{
			"requested wins over an already-valid record", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(90 * 24 * time.Hour), requestedAt: t1(-time.Hour)},
			nil, 30, ScreeningStatusRequested,
		},
		{
			"latest attempt rejected", true,
			screeningSnapshot{lastResult: result(vog.ResultRejected)},
			nil, 30, ScreeningStatusRejected,
		},
		{
			"latest attempt mismatch", true,
			screeningSnapshot{lastResult: result(vog.ResultMismatch)},
			nil, 30, ScreeningStatusRejected,
		},
		{
			"latest attempt insufficient scope", true,
			screeningSnapshot{lastResult: result(vog.ResultInsufficientScope)},
			nil, 30, ScreeningStatusRejected,
		},
		{
			"valid but missing a newly required code", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(90 * 24 * time.Hour), coveredCodes: []string{"11"}},
			[]string{"11", "43"},
			30, ScreeningStatusRecheckRequired,
		},
		{
			"valid and covers every required code", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(90 * 24 * time.Hour), coveredCodes: []string{"11", "43"}},
			[]string{"11", "43"},
			30, ScreeningStatusValid,
		},
		{
			"valid, no requirement to cover", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(90 * 24 * time.Hour)},
			nil, 30, ScreeningStatusValid,
		},
		{
			"expiring within lookahead", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(10 * 24 * time.Hour)},
			nil, 30, ScreeningStatusExpiring,
		},
		{
			"expiring exactly at the lookahead boundary", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(30 * 24 * time.Hour)},
			nil, 30, ScreeningStatusExpiring,
		},
		{
			"expired", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(-24 * time.Hour)},
			nil, 30, ScreeningStatusExpired,
		},
		{
			"expired exactly now", true,
			screeningSnapshot{lastResult: result(vog.ResultValid), validUntil: t1(0)},
			nil, 30, ScreeningStatusExpired,
		},
		{
			"valid with no expiry at all", true,
			screeningSnapshot{lastResult: result(vog.ResultValid)},
			nil, 30, ScreeningStatusValid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveScreeningStatus(tt.required, tt.snap, tt.codes, now, tt.lookahead)
			if got != tt.want {
				t.Errorf("DeriveScreeningStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCoversAll(t *testing.T) {
	if !coversAll(nil, nil) {
		t.Error("coversAll(nil, nil) = false, want true")
	}
	if !coversAll([]string{"11"}, []string{"11", "43"}) {
		t.Error("coversAll subset = false, want true")
	}
	if coversAll([]string{"11", "43"}, []string{"11"}) {
		t.Error("coversAll superset requirement = true, want false")
	}
}

func TestScreeningSettingsRequiredForMember(t *testing.T) {
	tests := []struct {
		requiredFor string
		employee    bool
		external    bool
	}{
		{ScreeningRequiredForNobody, false, false},
		{ScreeningRequiredForEmployees, true, false},
		{ScreeningRequiredForExternals, false, true},
		{ScreeningRequiredForBoth, true, true},
	}
	for _, tt := range tests {
		s := ScreeningSettings{RequiredFor: tt.requiredFor}
		if got := s.RequiredForMember(MemberTypeEmployee); got != tt.employee {
			t.Errorf("RequiredForMember(employee) for %q = %v, want %v", tt.requiredFor, got, tt.employee)
		}
		if got := s.RequiredForMember(MemberTypeExternal); got != tt.external {
			t.Errorf("RequiredForMember(external) for %q = %v, want %v", tt.requiredFor, got, tt.external)
		}
	}
}

func TestScreeningSettingsRecheckIntervalFor(t *testing.T) {
	employee, external := 12, 6
	s := ScreeningSettings{EmployeeRecheckIntervalMonths: &employee, ExternalRecheckIntervalMonths: &external}

	if got := s.RecheckIntervalFor(MemberTypeEmployee); got == nil || *got != employee {
		t.Errorf("RecheckIntervalFor(employee) = %v, want %d", got, employee)
	}
	if got := s.RecheckIntervalFor(MemberTypeExternal); got == nil || *got != external {
		t.Errorf("RecheckIntervalFor(external) = %v, want %d", got, external)
	}
}
