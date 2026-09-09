package organization

import (
	"testing"
	"time"
)

func TestDeriveIdentityStatus(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	t1 := func(delta time.Duration) *time.Time {
		v := now.Add(delta)
		return &v
	}

	tests := []struct {
		name       string
		verifiedAt *time.Time
		dueAt      *time.Time
		requested  *time.Time
		lookahead  int
		want       string
	}{
		{"never verified, no policy", nil, nil, nil, 30, IdentityStatusNever},
		{"verified, no due date (policy off)", t1(-time.Hour), nil, nil, 30, IdentityStatusVerified},
		{"verified, due date far away", t1(-time.Hour), t1(90 * 24 * time.Hour), nil, 30, IdentityStatusVerified},
		{"due soon, within lookahead", t1(-time.Hour), t1(10 * 24 * time.Hour), nil, 30, IdentityStatusDueSoon},
		{"due exactly at lookahead boundary", t1(-time.Hour), t1(30 * 24 * time.Hour), nil, 30, IdentityStatusDueSoon},
		{"overdue, due date in the past", t1(-400 * 24 * time.Hour), t1(-24 * time.Hour), nil, 30, IdentityStatusOverdue},
		{"overdue, due date exactly now", t1(-24 * time.Hour), t1(0), nil, 30, IdentityStatusOverdue},
		{"requested wins over overdue", t1(-400 * 24 * time.Hour), t1(-24 * time.Hour), t1(-time.Hour), 30, IdentityStatusRequested},
		{"requested wins over due soon", t1(-time.Hour), t1(10 * 24 * time.Hour), t1(-time.Hour), 30, IdentityStatusRequested},
		{"requested with no due date at all", nil, nil, t1(-time.Hour), 30, IdentityStatusRequested},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveIdentityStatus(tt.verifiedAt, tt.dueAt, tt.requested, now, tt.lookahead)
			if got != tt.want {
				t.Errorf("DeriveIdentityStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIdentitySettingsLookaheadDays(t *testing.T) {
	tests := []struct {
		name string
		s    IdentitySettings
		want int
	}{
		{"unconfigured falls back to default", IdentitySettings{}, reminderLookaheadDays},
		{"picks the largest threshold", IdentitySettings{ReminderDaysBefore: []int32{7, 30, 14}}, 30},
		{"single threshold", IdentitySettings{ReminderDaysBefore: []int32{14}}, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.LookaheadDays(); got != tt.want {
				t.Errorf("LookaheadDays() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIdentitySettingsIntervalFor(t *testing.T) {
	employee, external := 12, 6
	s := IdentitySettings{EmployeeIntervalMonths: &employee, ExternalIntervalMonths: &external}

	if got := s.IntervalFor(MemberTypeEmployee); got == nil || *got != employee {
		t.Errorf("IntervalFor(employee) = %v, want %d", got, employee)
	}
	if got := s.IntervalFor(MemberTypeExternal); got == nil || *got != external {
		t.Errorf("IntervalFor(external) = %v, want %d", got, external)
	}
	if got := (IdentitySettings{}).IntervalFor(MemberTypeEmployee); got != nil {
		t.Errorf("IntervalFor(employee) on empty settings = %v, want nil", got)
	}
}
