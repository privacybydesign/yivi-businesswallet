//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestSaveIdentitySettingsRecomputesDueDates(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	employee := createUser(t, pool, "employee@example.test")
	external := createUser(t, pool, "external@example.test")

	verifiedLongAgo := time.Now().AddDate(0, -13, 0)
	verifiedRecently := time.Now().AddDate(0, 0, -10)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at) VALUES ($1, $2, $3, 'employee', $4)`,
		employee, org.ID, organization.RoleMember, verifiedLongAgo); err != nil {
		t.Fatalf("insert employee membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at) VALUES ($1, $2, $3, 'external', $4)`,
		external, org.ID, organization.RoleMember, verifiedRecently); err != nil {
		t.Fatalf("insert external membership: %v", err)
	}

	employeeMonths, externalMonths := 12, 6
	settings, err := store.SaveIdentitySettings(ctx, org.ID, organization.IdentitySettingsInput{
		EmployeeIntervalMonths:      &employeeMonths,
		ExternalIntervalMonths:      &externalMonths,
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	})
	if err != nil {
		t.Fatalf("SaveIdentitySettings: %v", err)
	}
	if !settings.Configured || settings.EmployeeIntervalMonths == nil || *settings.EmployeeIntervalMonths != 12 {
		t.Errorf("SaveIdentitySettings result = %+v", settings)
	}

	// The employee was verified 13 months ago against a 12-month interval, so
	// their due date is already in the past; the external was verified 10 days
	// ago against a 6-month interval, so theirs is comfortably in the future.
	emp, err := store.GetMember(ctx, org.ID, employee)
	if err != nil {
		t.Fatalf("GetMember employee: %v", err)
	}
	if emp.IdentityDueAt == nil || !emp.IdentityDueAt.Before(time.Now()) {
		t.Errorf("employee identityDueAt = %v, want a past due date", emp.IdentityDueAt)
	}
	ext, err := store.GetMember(ctx, org.ID, external)
	if err != nil {
		t.Fatalf("GetMember external: %v", err)
	}
	if ext.IdentityDueAt == nil || !ext.IdentityDueAt.After(time.Now()) {
		t.Errorf("external identityDueAt = %v, want a future due date", ext.IdentityDueAt)
	}

	// Switching employees' policy off recomputes their due date back to nil,
	// leaving the external member (a different interval) untouched.
	if _, err := store.SaveIdentitySettings(ctx, org.ID, organization.IdentitySettingsInput{
		ExternalIntervalMonths:      &externalMonths,
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	}); err != nil {
		t.Fatalf("SaveIdentitySettings (employees off): %v", err)
	}
	emp, err = store.GetMember(ctx, org.ID, employee)
	if err != nil {
		t.Fatalf("GetMember employee after turning policy off: %v", err)
	}
	if emp.IdentityDueAt != nil {
		t.Errorf("employee identityDueAt = %v, want nil once the employee policy is off", emp.IdentityDueAt)
	}
	ext, err = store.GetMember(ctx, org.ID, external)
	if err != nil {
		t.Fatalf("GetMember external after turning employee policy off: %v", err)
	}
	if ext.IdentityDueAt == nil {
		t.Error("external identityDueAt = nil, want it to survive the employee-only settings change")
	}
}

func TestSaveIdentitySettingsRejectsInvalidInput(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	org := makeOrg(t, pool, "Acme", "acme")

	_, err := store.SaveIdentitySettings(context.Background(), org.ID, organization.IdentitySettingsInput{
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          "not_a_real_consequence",
	})
	if !errors.Is(err, organization.ErrIdentitySettingsInvalid) {
		t.Errorf("SaveIdentitySettings with a bad consequence = %v, want ErrIdentitySettingsInvalid", err)
	}
}

func TestGetIdentitySettingsUnconfiguredIsOff(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	org := makeOrg(t, pool, "Acme", "acme")

	settings, err := store.GetIdentitySettings(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("GetIdentitySettings: %v", err)
	}
	if settings.Configured {
		t.Error("Configured = true for an org that never saved identity settings")
	}
	if settings.EmployeeIntervalMonths != nil || settings.ExternalIntervalMonths != nil {
		t.Errorf("unconfigured settings = %+v, want both intervals nil", settings)
	}
	if settings.OverdueConsequence != organization.OverdueConsequenceFlag {
		t.Errorf("OverdueConsequence = %q, want flag-only by default", settings.OverdueConsequence)
	}
}

func TestRequestIdentificationSetsRequestedAtAndMintsToken(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	admin := createUser(t, pool, "admin@example.test")
	member := createUser(t, pool, "member@example.test")
	addMembership(t, pool, member, org.ID, nil)

	requested, err := store.RequestIdentification(ctx, org.ID, []uuid.UUID{member, uuid.New()}, admin, "annual check")
	if err != nil {
		t.Fatalf("RequestIdentification: %v", err)
	}
	// The unknown second id is silently skipped (see the method's doc); only the
	// real member comes back.
	if len(requested) != 1 || requested[0].UserID != member || requested[0].Email != "member@example.test" {
		t.Fatalf("RequestIdentification = %+v, want exactly the known member", requested)
	}
	if requested[0].ReidentifyToken == "" {
		t.Error("ReidentifyToken is empty")
	}

	got, err := store.GetMember(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if got.IdentityRequestedAt == nil || got.IdentityRequestedBy == nil || *got.IdentityRequestedBy != admin {
		t.Errorf("member = %+v, want identityRequestedAt/By set to the admin", got)
	}

	rc, err := store.ReverifyTokenLookup(ctx, requested[0].ReidentifyToken)
	if err != nil {
		t.Fatalf("ReverifyTokenLookup: %v", err)
	}
	if rc.UserID != member || rc.OrganizationID != org.ID || rc.Email != "member@example.test" {
		t.Errorf("ReverifyTokenLookup = %+v", rc)
	}
}

func TestEnsureReverifyTokenRotatesTheLiveToken(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	member := createUser(t, pool, "member@example.test")
	addMembership(t, pool, member, org.ID, nil)

	first, _, err := store.EnsureReverifyToken(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("EnsureReverifyToken (first): %v", err)
	}
	second, _, err := store.EnsureReverifyToken(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("EnsureReverifyToken (second): %v", err)
	}
	if first == second {
		t.Fatal("EnsureReverifyToken returned the same token twice")
	}

	if _, err := store.ReverifyTokenLookup(ctx, first); !errors.Is(err, organization.ErrReverifyTokenNotFound) {
		t.Errorf("lookup of the rotated-away token = %v, want ErrReverifyTokenNotFound", err)
	}
	if _, err := store.ReverifyTokenLookup(ctx, second); err != nil {
		t.Errorf("lookup of the live token: %v", err)
	}
}

func TestReverifyTokenLookupUnknownToken(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})

	if _, err := store.ReverifyTokenLookup(context.Background(), "not-a-real-token"); !errors.Is(err, organization.ErrReverifyTokenNotFound) {
		t.Errorf("ReverifyTokenLookup = %v, want ErrReverifyTokenNotFound", err)
	}
}

func TestCompleteReverificationUpdatesMembershipAndClearsRequest(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	admin := createUser(t, pool, "admin@example.test")
	member := createUser(t, pool, "alice@example.test")
	oldVerifiedAt := time.Now().AddDate(-1, 0, 0)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at) VALUES ($1, $2, $3, 'employee', $4)`,
		member, org.ID, organization.RoleMember, oldVerifiedAt); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET given_names = $2, last_name = $3 WHERE id = $1`,
		member, "Alice", "Anderson"); err != nil {
		t.Fatalf("set user name: %v", err)
	}

	months := 12
	if _, err := store.SaveIdentitySettings(ctx, org.ID, organization.IdentitySettingsInput{
		EmployeeIntervalMonths: &months, OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4,
		OverdueConsequence: organization.OverdueConsequenceFlag,
	}); err != nil {
		t.Fatalf("SaveIdentitySettings: %v", err)
	}

	requested, err := store.RequestIdentification(ctx, org.ID, []uuid.UUID{member}, admin, "")
	if err != nil {
		t.Fatalf("RequestIdentification: %v", err)
	}
	token := requested[0].ReidentifyToken

	before, err := store.GetMember(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("GetMember before: %v", err)
	}
	if before.IdentityDueAt == nil || !before.IdentityDueAt.Before(time.Now()) {
		t.Fatalf("precondition: due date should already be overdue, got %v", before.IdentityDueAt)
	}

	if err := store.CompleteReverification(ctx, org.ID, member, identity.Name{GivenNames: "Alice", LastName: "Anderson"}, "+31612345678", "1990-01-01"); err != nil {
		t.Fatalf("CompleteReverification: %v", err)
	}

	after, err := store.GetMember(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("GetMember after: %v", err)
	}
	if after.IdentityVerifiedAt == nil || !after.IdentityVerifiedAt.After(oldVerifiedAt) {
		t.Errorf("identityVerifiedAt = %v, want it refreshed to now", after.IdentityVerifiedAt)
	}
	if after.IdentityRequestedAt != nil || after.IdentityRequestedBy != nil {
		t.Errorf("identityRequestedAt/By = %v/%v, want both cleared", after.IdentityRequestedAt, after.IdentityRequestedBy)
	}
	if after.IdentityDueAt == nil || !after.IdentityDueAt.After(time.Now()) {
		t.Errorf("identityDueAt = %v, want a fresh future due date under the 12-month policy", after.IdentityDueAt)
	}
	if after.Phone == nil || *after.Phone != "+31612345678" {
		t.Errorf("phone = %v, want the disclosed number", after.Phone)
	}

	// The token that was used is retired: it cannot be replayed.
	if _, err := store.ReverifyTokenLookup(ctx, token); !errors.Is(err, organization.ErrReverifyTokenNotFound) {
		t.Errorf("ReverifyTokenLookup after use = %v, want ErrReverifyTokenNotFound", err)
	}
}

func TestUpdateMemberTypeRecomputesDueDate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	member := createUser(t, pool, "member@example.test")
	verifiedAt := time.Now().AddDate(0, -8, 0)
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at) VALUES ($1, $2, $3, 'employee', $4)`,
		member, org.ID, organization.RoleMember, verifiedAt); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	employeeMonths, externalMonths := 12, 3
	if _, err := store.SaveIdentitySettings(ctx, org.ID, organization.IdentitySettingsInput{
		EmployeeIntervalMonths: &employeeMonths, ExternalIntervalMonths: &externalMonths,
		OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4, OverdueConsequence: organization.OverdueConsequenceFlag,
	}); err != nil {
		t.Fatalf("SaveIdentitySettings: %v", err)
	}

	before, err := store.GetMember(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("GetMember before: %v", err)
	}
	if before.IdentityDueAt == nil || !before.IdentityDueAt.After(time.Now()) {
		t.Fatalf("precondition: employee (12mo, verified 8mo ago) should not be due yet, got %v", before.IdentityDueAt)
	}

	// Reclassifying as external (3-month interval, verified 8 months ago) should
	// push the due date into the past.
	if _, err := store.UpdateMemberType(ctx, org.ID, member, organization.MemberTypeExternal, strptr("Contractors BV")); err != nil {
		t.Fatalf("UpdateMemberType: %v", err)
	}
	after, err := store.GetMember(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("GetMember after: %v", err)
	}
	if after.MemberType != organization.MemberTypeExternal || after.ExternalOrganisation == nil || *after.ExternalOrganisation != "Contractors BV" {
		t.Errorf("member = %+v, want external at Contractors BV", after)
	}
	if after.IdentityDueAt == nil || !after.IdentityDueAt.Before(time.Now()) {
		t.Errorf("identityDueAt after switching to external = %v, want it now overdue", after.IdentityDueAt)
	}
}

func TestIdentityReminderCandidatesAndRecordSent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	overdueUser := createUser(t, pool, "overdue@example.test")
	dueSoonUser := createUser(t, pool, "duesoon@example.test")
	notDueUser := createUser(t, pool, "notdue@example.test")

	insert := func(userID uuid.UUID, dueAt time.Time) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at, identity_due_at) VALUES ($1, $2, $3, 'employee', now(), $4)`,
			userID, org.ID, organization.RoleMember, dueAt); err != nil {
			t.Fatalf("insert membership for %s: %v", userID, err)
		}
	}
	insert(overdueUser, time.Now().AddDate(0, 0, -5))
	insert(dueSoonUser, time.Now().AddDate(0, 0, 5))
	insert(notDueUser, time.Now().AddDate(0, 0, 90))

	settings := organization.IdentitySettings{
		Configured:                  true,
		ReminderDaysBefore:          []int32{30, 14, 7},
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
	}

	candidates, err := store.IdentityReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		t.Fatalf("IdentityReminderCandidates: %v", err)
	}
	byUser := map[uuid.UUID]organization.ReminderCandidate{}
	for _, c := range candidates {
		byUser[c.UserID] = c
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want exactly the overdue and due-soon members", candidates)
	}
	if c, ok := byUser[overdueUser]; !ok || !c.Overdue {
		t.Errorf("overdue candidate = %+v, ok=%v", c, ok)
	}
	if c, ok := byUser[dueSoonUser]; !ok || c.Overdue {
		t.Errorf("due-soon candidate = %+v, ok=%v", c, ok)
	}
	if _, ok := byUser[notDueUser]; ok {
		t.Error("a member due in 90 days was selected as a reminder candidate")
	}

	// Recording a send for the due-soon member takes them out of the next pass
	// (their one pre-due reminder already went out).
	if err := store.RecordIdentityReminderSent(ctx, org.ID, dueSoonUser, "duesoon@example.test", false); err != nil {
		t.Fatalf("RecordIdentityReminderSent: %v", err)
	}
	again, err := store.IdentityReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		t.Fatalf("IdentityReminderCandidates (second pass): %v", err)
	}
	for _, c := range again {
		if c.UserID == dueSoonUser {
			t.Error("the due-soon member is still a candidate after their reminder was recorded")
		}
	}

	// The overdue member is still a candidate (no reminder recorded yet).
	found := false
	for _, c := range again {
		if c.UserID == overdueUser {
			found = true
		}
	}
	if !found {
		t.Error("the overdue member dropped out without a reminder being recorded for them")
	}

	// Recording an overdue reminder resets the cadence window: they will not be
	// selected again until OverdueReminderIntervalDays has passed.
	if err := store.RecordIdentityReminderSent(ctx, org.ID, overdueUser, "overdue@example.test", true); err != nil {
		t.Fatalf("RecordIdentityReminderSent (overdue): %v", err)
	}
	third, err := store.IdentityReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		t.Fatalf("IdentityReminderCandidates (third pass): %v", err)
	}
	if len(third) != 0 {
		t.Errorf("candidates after both reminders recorded = %+v, want none", third)
	}
}

func TestIdentityBlockedSetAndIsIdentityBlocked(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	blockOrg := makeOrg(t, pool, "Blocker", "blocker")
	flagOrg := makeOrg(t, pool, "Flagger", "flagger")
	overdueInBlockOrg := createUser(t, pool, "overdue-block@example.test")
	overdueInFlagOrg := createUser(t, pool, "overdue-flag@example.test")

	// Verified 13 months ago, so the 12-month policy saved below recomputes both
	// members' due dates into the past — which is what makes them overdue.
	verifiedLongAgo := time.Now().AddDate(0, -13, 0)
	for _, x := range []struct {
		orgID  uuid.UUID
		userID uuid.UUID
	}{{blockOrg.ID, overdueInBlockOrg}, {flagOrg.ID, overdueInFlagOrg}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at) VALUES ($1, $2, $3, 'employee', $4)`,
			x.userID, x.orgID, organization.RoleMember, verifiedLongAgo); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
	}

	months := 12
	if _, err := store.SaveIdentitySettings(ctx, blockOrg.ID, organization.IdentitySettingsInput{
		EmployeeIntervalMonths:      &months,
		OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4, OverdueConsequence: organization.OverdueConsequenceBlock,
	}); err != nil {
		t.Fatalf("SaveIdentitySettings blockOrg: %v", err)
	}
	if _, err := store.SaveIdentitySettings(ctx, flagOrg.ID, organization.IdentitySettingsInput{
		EmployeeIntervalMonths:      &months,
		OverdueReminderIntervalDays: 7, OverdueReminderMaxCount: 4, OverdueConsequence: organization.OverdueConsequenceFlag,
	}); err != nil {
		t.Fatalf("SaveIdentitySettings flagOrg: %v", err)
	}

	blocked, err := store.IsIdentityBlocked(ctx, blockOrg.ID, overdueInBlockOrg)
	if err != nil {
		t.Fatalf("IsIdentityBlocked (block policy): %v", err)
	}
	if !blocked {
		t.Error("an overdue member under a block policy is not blocked")
	}

	notBlocked, err := store.IsIdentityBlocked(ctx, flagOrg.ID, overdueInFlagOrg)
	if err != nil {
		t.Fatalf("IsIdentityBlocked (flag policy): %v", err)
	}
	if notBlocked {
		t.Error("an overdue member under a flag-only policy is blocked")
	}

	set, err := store.IdentityBlockedSet(ctx, blockOrg.ID)
	if err != nil {
		t.Fatalf("IdentityBlockedSet: %v", err)
	}
	if !set[overdueInBlockOrg] {
		t.Errorf("IdentityBlockedSet = %v, want %s in it", set, overdueInBlockOrg)
	}

	emptySet, err := store.IdentityBlockedSet(ctx, flagOrg.ID)
	if err != nil {
		t.Fatalf("IdentityBlockedSet (flag org): %v", err)
	}
	if len(emptySet) != 0 {
		t.Errorf("IdentityBlockedSet for a flag-only org = %v, want empty", emptySet)
	}
}
