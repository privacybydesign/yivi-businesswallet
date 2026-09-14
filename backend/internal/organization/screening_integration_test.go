//go:build integration

package organization_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

func TestRecordScreeningValidUpdatesMembershipState(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	userID := createUser(t, pool, "member@example.test")
	dob := time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, date_of_birth) VALUES ($1, $2, $3, 'employee', $4)`,
		userID, org.ID, organization.RoleMember, dob); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	settings := organization.ScreeningSettings{RecheckAnchor: organization.RecheckAnchorIssueDate, EmployeeRecheckIntervalMonths: intPtr(12)}
	issueDate := time.Now().AddDate(0, -1, 0)
	in := organization.ScreeningInput{
		Method: vog.MethodPDF, Result: vog.ResultValid, CheckedBy: organization.CheckedBySelf,
		VogIssueDate: &issueDate, Reference: "REF-1", CoveredCodes: []string{"11", "43"},
	}
	if err := store.RecordScreening(ctx, org.ID, userID, organization.MemberTypeEmployee, settings, in, []byte("test-key")); err != nil {
		t.Fatalf("RecordScreening: %v", err)
	}

	m, err := store.GetMember(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m.VogLastResult == nil || *m.VogLastResult != string(vog.ResultValid) {
		t.Errorf("VogLastResult = %v, want valid", m.VogLastResult)
	}
	if m.VogValidUntil == nil || !m.VogValidUntil.After(time.Now()) {
		t.Errorf("VogValidUntil = %v, want a future date (issue date + 12 months)", m.VogValidUntil)
	}
	wantValidUntil := issueDate.AddDate(1, 0, 0)
	if m.VogValidUntil == nil || m.VogValidUntil.Sub(wantValidUntil).Abs() > time.Minute {
		t.Errorf("VogValidUntil = %v, want ~%v", m.VogValidUntil, wantValidUntil)
	}
	if want := []string{"11", "43"}; !equalStrings(m.VogCoveredCodes, want) {
		t.Errorf("VogCoveredCodes = %v, want %v", m.VogCoveredCodes, want)
	}

	history, err := store.ListScreeningHistory(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("ListScreeningHistory: %v", err)
	}
	if len(history) != 1 || history[0].Result != string(vog.ResultValid) {
		t.Fatalf("history = %+v, want one valid record", history)
	}
}

func TestRecordScreeningRejectedClearsValidUntil(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	userID := createUser(t, pool, "member@example.test")
	dob := time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, date_of_birth) VALUES ($1, $2, $3, 'employee', $4)`,
		userID, org.ID, organization.RoleMember, dob); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	settings := organization.ScreeningSettings{EmployeeRecheckIntervalMonths: intPtr(12)}
	issueDate := time.Now().AddDate(0, -1, 0)

	// First a valid screening sets vog_valid_until in the future...
	valid := organization.ScreeningInput{Method: vog.MethodPDF, Result: vog.ResultValid, CheckedBy: organization.CheckedBySelf, VogIssueDate: &issueDate}
	if err := store.RecordScreening(ctx, org.ID, userID, organization.MemberTypeEmployee, settings, valid, nil); err != nil {
		t.Fatalf("RecordScreening (valid): %v", err)
	}

	// ...then a subsequent mismatch clears it, per the "latest attempt wins" rule.
	mismatch := organization.ScreeningInput{Method: vog.MethodPDF, Result: vog.ResultMismatch, CheckedBy: organization.CheckedBySelf, VogIssueDate: &issueDate}
	if err := store.RecordScreening(ctx, org.ID, userID, organization.MemberTypeEmployee, settings, mismatch, nil); err != nil {
		t.Fatalf("RecordScreening (mismatch): %v", err)
	}

	m, err := store.GetMember(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m.VogLastResult == nil || *m.VogLastResult != string(vog.ResultMismatch) {
		t.Errorf("VogLastResult = %v, want mismatch", m.VogLastResult)
	}
	if m.VogValidUntil != nil {
		t.Errorf("VogValidUntil = %v, want nil after a failed re-check", m.VogValidUntil)
	}

	history, err := store.ListScreeningHistory(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("ListScreeningHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history has %d rows, want 2 (both attempts kept)", len(history))
	}
}

func TestSaveScreeningSettingsRecomputesValidUntil(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	userID := createUser(t, pool, "member@example.test")
	dob := time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, date_of_birth) VALUES ($1, $2, $3, 'employee', $4)`,
		userID, org.ID, organization.RoleMember, dob); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	issueDate := time.Now().AddDate(-1, 0, 0)
	in := organization.ScreeningInput{Method: vog.MethodPDF, Result: vog.ResultValid, CheckedBy: organization.CheckedBySelf, VogIssueDate: &issueDate}
	if err := store.RecordScreening(ctx, org.ID, userID, organization.MemberTypeEmployee, organization.ScreeningSettings{}, in, nil); err != nil {
		t.Fatalf("RecordScreening: %v", err)
	}

	// With no recheck interval configured, valid_until starts nil.
	m, err := store.GetMember(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m.VogValidUntil != nil {
		t.Errorf("VogValidUntil = %v, want nil before any interval is configured", m.VogValidUntil)
	}

	// Configuring a 6-month interval recomputes it from the stored issue date -
	// one year ago, so 6 months puts it solidly in the past.
	if _, err := store.SaveScreeningSettings(ctx, org.ID, organization.ScreeningSettingsInput{
		RequiredFor: organization.ScreeningRequiredForBoth, RecheckAnchor: organization.RecheckAnchorIssueDate,
		EmployeeRecheckIntervalMonths: intPtr(6), OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4,
		OverdueConsequence: organization.OverdueConsequenceFlag,
	}); err != nil {
		t.Fatalf("SaveScreeningSettings: %v", err)
	}

	m, err = store.GetMember(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("GetMember after settings save: %v", err)
	}
	if m.VogValidUntil == nil || !m.VogValidUntil.Before(time.Now()) {
		t.Errorf("VogValidUntil = %v, want a past date (issue date + 6 months)", m.VogValidUntil)
	}
}

func TestRequestVogSingleAndBulk(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	admin := createUser(t, pool, "admin@example.test")
	m1 := createUser(t, pool, "m1@example.test")
	m2 := createUser(t, pool, "m2@example.test")
	notAMember := createUser(t, pool, "outsider@example.test")
	for _, u := range []uuid.UUID{m1, m2} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, organization_id, role, member_type) VALUES ($1, $2, $3, 'employee')`,
			u, org.ID, organization.RoleMember); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
	}

	requested, err := store.RequestVog(ctx, org.ID, []uuid.UUID{m1, m2, notAMember}, admin, "annual re-check")
	if err != nil {
		t.Fatalf("RequestVog: %v", err)
	}
	if len(requested) != 2 {
		t.Fatalf("requested %d members, want 2 (the non-member must be skipped)", len(requested))
	}

	member, err := store.GetMember(ctx, org.ID, m1)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if member.VogRequestedAt == nil {
		t.Error("VogRequestedAt = nil, want set after RequestVog")
	}
	if member.VogRequestedBy == nil || *member.VogRequestedBy != admin {
		t.Errorf("VogRequestedBy = %v, want %v", member.VogRequestedBy, admin)
	}
}

func TestVogReminderCandidatesRespectsCadence(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	expiringSoon := createUser(t, pool, "soon@example.test")
	alreadyExpired := createUser(t, pool, "expired@example.test")

	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, vog_last_result, vog_valid_until)
		 VALUES ($1, $2, $3, 'employee', 'valid', now() + interval '5 days')`,
		expiringSoon, org.ID, organization.RoleMember); err != nil {
		t.Fatalf("insert expiring membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, vog_last_result, vog_valid_until)
		 VALUES ($1, $2, $3, 'employee', 'valid', now() - interval '1 day')`,
		alreadyExpired, org.ID, organization.RoleMember); err != nil {
		t.Fatalf("insert expired membership: %v", err)
	}

	settings := organization.ScreeningSettings{
		ReminderDaysBefore: []int32{30}, OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4,
	}
	candidates, err := store.VogReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		t.Fatalf("VogReminderCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want 2", candidates)
	}

	for _, c := range candidates {
		if err := store.RecordVogReminderSent(ctx, org.ID, c.UserID, c.Email, c.Overdue); err != nil {
			t.Fatalf("RecordVogReminderSent: %v", err)
		}
	}

	// Sweeping again immediately finds nothing: the due-soon member already got
	// its one advance reminder, and the overdue member is inside its cadence
	// window.
	candidates, err = store.VogReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		t.Fatalf("VogReminderCandidates (second sweep): %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("candidates = %+v, want none on an immediate re-sweep", candidates)
	}
}

func intPtr(i int) *int { return &i }
