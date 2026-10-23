package proofing

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

var legalFiveYears = LegalRetention{
	Months:   60,
	ReasonEN: "The Wwft requires us to keep proof of identity for five years.",
	ReasonNL: "De Wwft verplicht ons het identiteitsbewijs vijf jaar te bewaren.",
}

// A legal period counts calendar months as the database does, so a check that
// ended on the 31st is held to the last day of a shorter month.
func TestAddMonths(t *testing.T) {
	at := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, 9, 30, 0, 0, time.UTC)
	}
	for name, tc := range map[string]struct {
		start  time.Time
		months int
		want   time.Time
	}{
		"five years":         {at(2026, time.October, 19), 60, at(2031, time.October, 19)},
		"a month":            {at(2026, time.October, 19), 1, at(2026, time.November, 19)},
		"past a year's end":  {at(2026, time.November, 30), 3, at(2027, time.February, 28)},
		"the 31st":           {at(2026, time.January, 31), 1, at(2026, time.February, 28)},
		"a leap day":         {at(2028, time.February, 29), 12, at(2029, time.February, 28)},
		"a leap day, a leap": {at(2028, time.February, 29), 48, at(2032, time.February, 29)},
	} {
		if got := addMonths(tc.start, tc.months); !got.Equal(tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestLegalRetentionCheck(t *testing.T) {
	tooLong := strings.Repeat("é", maxLegalReasonLength+1)
	for name, tc := range map[string]struct {
		legal LegalRetention
		ok    bool
	}{
		"none removes it":      {LegalRetention{}, true},
		"five years":           {legalFiveYears, true},
		"the longest":          {LegalRetention{Months: maxLegalRetentionMonths, ReasonEN: "a", ReasonNL: "b"}, true},
		"a reason at its size": {LegalRetention{Months: 1, ReasonEN: strings.Repeat("é", maxLegalReasonLength), ReasonNL: "b"}, true},
		"no months":            {LegalRetention{ReasonEN: "a", ReasonNL: "b"}, false},
		"negative months":      {LegalRetention{Months: -1, ReasonEN: "a", ReasonNL: "b"}, false},
		"past the longest":     {LegalRetention{Months: maxLegalRetentionMonths + 1, ReasonEN: "a", ReasonNL: "b"}, false},
		"no English reason":    {LegalRetention{Months: 60, ReasonNL: "b"}, false},
		"no Dutch reason":      {LegalRetention{Months: 60, ReasonEN: "a"}, false},
		"a blank reason":       {LegalRetention{Months: 60, ReasonEN: "a", ReasonNL: "  "}, false},
		"a reason too long":    {LegalRetention{Months: 60, ReasonEN: tooLong, ReasonNL: "b"}, false},
	} {
		err := tc.legal.check()
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want it accepted", name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
}

// An identity check is kept for the legal period whatever its flow says: the
// flow keeps it longer, never shorter. Any other kind of session is not held.
func TestEngineRetentionLegalFloor(t *testing.T) {
	settings := Settings{Session: SessionSettings{DataRetentionDays: 30}, LegalRetention: legalFiveYears}
	week, year := 7*24*time.Hour, 365*24*time.Hour
	legal := legalFiveYears.longest()
	if legal <= year {
		t.Fatalf("five years last %v, no longer than a year", legal)
	}
	for name, override := range map[string]time.Duration{"the org's": 0, "a week's flow": week, "a year's flow": year} {
		if got := engineRetention(&settings, FlowIdentity, override); got != legal+retentionGrace {
			t.Errorf("%s: %v, want the legal period plus grace", name, got)
		}
	}
	if got := engineRetention(&settings, FlowDataErasure, week); got != week+retentionGrace {
		t.Errorf("an erasure request: %v, want its flow's retention", got)
	}
	short := Settings{Session: SessionSettings{DataRetentionDays: 365}, LegalRetention: LegalRetention{Months: 1, ReasonEN: "a", ReasonNL: "b"}}
	if got := engineRetention(&short, FlowIdentity, 0); got != year+retentionGrace {
		t.Errorf("a month's law under a year's retention: %v, want the year", got)
	}
}

// The person is told the calendar's days: five years from a day in 2026 are
// 1826, and the engine's grace adds one.
func TestSubjectRetentionDaysLegal(t *testing.T) {
	settings := Settings{Session: SessionSettings{DataRetentionDays: 30}, LegalRetention: legalFiveYears}
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	if got := subjectRetentionDays(settings, FlowIdentity, 0, now); got != 1827 {
		t.Errorf("identity check: %d days, want 1827", got)
	}
	if got := subjectRetentionDays(settings, FlowDataAccess, 0, now); got != 31 {
		t.Errorf("data request: %d days, want the org's 31", got)
	}
	settings.LegalRetention = LegalRetention{}
	if got := subjectRetentionDays(settings, FlowIdentity, 0, now); got != 31 {
		t.Errorf("no legal retention: %d days, want the org's 31", got)
	}
}

// Saving a legal retention changes nothing until every field of the update is
// valid, and removing it takes the zero value.
func TestUpdateLegalRetention(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &legalFiveYears}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got := f.orgSettings.settings.LegalRetention; got != legalFiveYears {
		t.Errorf("saved %+v, want %+v", got, legalFiveYears)
	}

	padded := LegalRetention{Months: 12, ReasonEN: "  kept  ", ReasonNL: "\tbewaard\n"}
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &padded}); err != nil {
		t.Fatalf("UpdateSettings padded: %v", err)
	}
	if got := f.orgSettings.settings.LegalRetention; got.ReasonEN != "kept" || got.ReasonNL != "bewaard" {
		t.Errorf("saved reasons %q and %q, want them trimmed", got.ReasonEN, got.ReasonNL)
	}

	// An invalid legal retention beside a valid session change saves neither.
	ninety := 90
	bad := LegalRetention{Months: 0, ReasonEN: "a"}
	retentionBefore, legalBefore := f.orgSettings.settings.Session, f.orgSettings.settings.LegalRetention
	_, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{DataRetentionDays: &ninety, LegalRetention: &bad})
	if got := f.orgSettings.settings; !errors.Is(err, ErrInvalidInput) || got.Session != retentionBefore || got.LegalRetention != legalBefore {
		t.Errorf("a bad legal retention = %v, settings %+v; want ErrInvalidInput and no change", err, got)
	}

	none := LegalRetention{}
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &none}); err != nil {
		t.Fatalf("UpdateSettings none: %v", err)
	}
	if got := f.orgSettings.settings.LegalRetention; got != none {
		t.Errorf("after removing it: %+v", got)
	}
}

// Saving a legal retention gives the engine the retention of the org's identity
// checks: the longer of the legal period and what the check was sent with, plus
// the grace. Another setting's save does not ask the engine for anything.
func TestLegalSyncsEngineRetention(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	flowOverride := 100 * 24 * time.Hour
	f.requests.identitySessions = map[time.Duration][]string{0: {"a", "b"}, flowOverride: {"c"}}

	ninety := 90
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{DataRetentionDays: &ninety}); err != nil {
		t.Fatalf("UpdateSettings retention: %v", err)
	}
	if len(f.ips.retentions) != 0 {
		t.Fatalf("a retention change set %v in the engine; want nothing", f.ips.retentions)
	}

	oneMonth := LegalRetention{Months: 1, ReasonEN: "Kept.", ReasonNL: "Bewaard."}
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &oneMonth}); err != nil {
		t.Fatalf("UpdateSettings legal retention: %v", err)
	}
	got := f.ips.retentions
	slices.SortFunc(got, func(a, b engineRetentionCall) int { return cmp.Compare(a.retention, b.retention) })
	// The org's 90 days outlast the month; the flow's 100 days outlast both.
	want := []engineRetentionCall{
		{ids: []string{"a", "b"}, retention: 90*24*time.Hour + retentionGrace},
		{ids: []string{"c"}, retention: flowOverride + retentionGrace},
	}
	if !slices.EqualFunc(got, want, func(a, b engineRetentionCall) bool {
		return a.retention == b.retention && slices.Equal(a.ids, b.ids)
	}) {
		t.Errorf("engine retentions = %+v; want %+v", got, want)
	}

	// A legal period longer than both outlasts the org's retention and the flow's.
	f.ips.retentions = nil
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &legalFiveYears}); err != nil {
		t.Fatalf("UpdateSettings five years: %v", err)
	}
	fiveYears := legalFiveYears.longest() + retentionGrace
	for _, call := range f.ips.retentions {
		if call.retention != fiveYears {
			t.Errorf("engine retention of %v = %v; want the legal period's %v", call.ids, call.retention, fiveYears)
		}
	}
}

// A run that failed leaves the legal retention saved, the update answered and
// the org marked, and the pruner's next run repeats it without the admin
// saving again. Many sessions go in batches.
func TestEngineRetentionRepeats(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	ids := make([]string, 2*engineSessionBatch+1)
	for i := range ids {
		ids[i] = strconv.Itoa(i)
	}
	f.requests.identitySessions = map[time.Duration][]string{0: ids}

	f.ips.retentionErr = errors.New("engine down")
	if _, err := f.svc.UpdateSettings(ctx, testOrg.ID, SettingsChange{LegalRetention: &legalFiveYears}); err != nil {
		t.Fatalf("UpdateSettings with the engine down = %v; want the change saved and answered", err)
	}
	if got := f.orgSettings.settings.LegalRetention; got != legalFiveYears {
		t.Errorf("saved %+v; want the legal retention saved all the same", got)
	}
	if !f.orgSettings.stale {
		t.Fatal("the org is not marked for the pruner after a failed sync")
	}

	f.ips.retentionErr, f.ips.retentions = nil, nil
	if _, err := f.svc.PurgeDue(ctx); err != nil {
		t.Fatalf("PurgeDue: %v", err)
	}
	var sizes []int
	for _, call := range f.ips.retentions {
		sizes = append(sizes, len(call.ids))
	}
	if want := []int{engineSessionBatch, engineSessionBatch, 1}; !slices.Equal(sizes, want) {
		t.Errorf("sessions per call = %v; want %v", sizes, want)
	}
	if f.orgSettings.stale {
		t.Error("the org is still marked after the pruner synced it")
	}

	// With nothing marked, the pruner asks the engine for nothing.
	f.ips.retentions = nil
	if _, err := f.svc.PurgeDue(ctx); err != nil || len(f.ips.retentions) != 0 {
		t.Errorf("PurgeDue with nothing marked = %v, set %v in the engine; want nothing", err, f.ips.retentions)
	}
}

// A check created against settings that changed before it was attached holds
// the retention it was created with, and a sync that ran meanwhile did not see
// it: the org is marked for the pruner. One created against the settings as
// they stand marks nothing.
func TestSessionCreatedDuringLegalSaveIsSynced(t *testing.T) {
	f := newFixture()
	f.sendExternal(t, "anna@example.org", "")
	if f.orgSettings.stale {
		t.Fatal("a check sent against the settings as they stand marked the org")
	}

	f.ips.onCreate = func() { f.orgSettings.settings.LegalRetention = legalFiveYears }
	f.sendExternal(t, "bob@example.org", "")
	if !f.orgSettings.stale {
		t.Fatal("a check created as the legal retention was saved did not mark the org")
	}
	f.requests.identitySessions = map[time.Duration][]string{0: {"ses-bob"}}
	if _, err := f.svc.PurgeDue(context.Background()); err != nil {
		t.Fatalf("PurgeDue: %v", err)
	}
	if want := legalFiveYears.longest() + retentionGrace; len(f.ips.retentions) != 1 || f.ips.retentions[0].retention != want {
		t.Errorf("engine retentions = %+v; want the session given the legal period's %v", f.ips.retentions, want)
	}
}

// A failed engine run does not cost the update its other fields: the origins
// that come with the legal retention are saved too.
func TestEngineFailureKeepsOrigins(t *testing.T) {
	f := newFixture()
	f.requests.identitySessions = map[time.Duration][]string{0: {"a"}}
	f.ips.retentionErr = errors.New("engine down")
	origins := []string{"https://example.org"}

	change := SettingsChange{LegalRetention: &legalFiveYears, RedirectOrigins: &origins}
	if _, err := f.svc.UpdateSettings(context.Background(), testOrg.ID, change); err != nil {
		t.Fatalf("UpdateSettings with the engine down = %v; want the change saved", err)
	}
	if got := f.orgSettings.settings.RedirectOrigins; !slices.Equal(got, origins) {
		t.Errorf("saved origins %v; want %v", got, origins)
	}
}

// An erasure takes no session the legal retention still holds: choosing none
// leaves it out, and ticking one is refused.
func TestApprovedMatchesLegalHold(t *testing.T) {
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	held, free := uuid.New(), uuid.New()
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	matches := []DataMatch{
		{RequestID: held, Level: MatchStrong, HeldUntil: &later},
		{RequestID: free, Level: MatchStrong, HeldUntil: &earlier},
	}
	got, err := approvedMatches(matches, nil, FlowDataErasure, now)
	if err != nil || !slices.Equal(got, []uuid.UUID{free}) {
		t.Errorf("erasure, none chosen = %v, %v; want only the session whose period passed", got, err)
	}
	if _, err := approvedMatches(matches, []uuid.UUID{held}, FlowDataErasure, now); !errors.Is(err, ErrLegalRetention) {
		t.Errorf("erasure ticking a held session = %v; want ErrLegalRetention", err)
	}
	if got, err := approvedMatches(matches, []uuid.UUID{free}, FlowDataErasure, now); err != nil || !slices.Equal(got, []uuid.UUID{free}) {
		t.Errorf("erasure ticking a free session = %v, %v", got, err)
	}
	// "See my data" keeps working: the law keeps the data, not from the person.
	if got, err := approvedMatches(matches, []uuid.UUID{held}, FlowDataAccess, now); err != nil || !slices.Equal(got, []uuid.UUID{held}) {
		t.Errorf("access ticking a held session = %v, %v; want it taken", got, err)
	}
}

// legalFixture is an erasure request whose person matched two sessions, which
// the org's legal retention holds until the given times.
func legalFixture(t *testing.T, firstUntil, secondUntil time.Duration) (fixture, *fakeDataRequests, Request, Request) {
	t.Helper()
	return dataLegalFixture(t, FlowDataErasure, firstUntil, secondUntil)
}

// dataLegalFixture is legalFixture for a data request of kind.
func dataLegalFixture(t *testing.T, kind FlowKind, firstUntil, secondUntil time.Duration) (fixture, *fakeDataRequests, Request, Request) {
	t.Helper()
	f, data := newDataFixture(kind)
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	f.orgSettings.settings.LegalRetention = legalFiveYears
	first, second := heldSession("Anna Jansen"), heldSession("Anna Jansen")
	data.candidates = []Request{first, second}
	data.matched = []Request{first, second}
	data.heldUntil = map[uuid.UUID]time.Time{}
	for id, d := range map[uuid.UUID]time.Duration{first.ID: firstUntil, second.ID: secondUntil} {
		data.heldUntil[id] = now.Add(d)
	}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	return f, data, first, second
}

// When the legal retention holds every session found, nothing could be
// erased, so the request is declined at once with the reason, and no session
// is touched.
func TestErasureDeclinedIfAllHeld(t *testing.T) {
	year := 365 * 24 * time.Hour
	f, data, _, _ := legalFixture(t, year, 2*year)
	deletes := f.ips.deletes

	req := f.reconcile(t)
	if req.Status != StatusRejected || req.ErrorCode != ErrorLegalRetention {
		t.Fatalf("request = %q %q; want rejected %s", req.Status, req.ErrorCode, ErrorLegalRetention)
	}
	if len(data.approved) != 0 || f.ips.deletes != deletes {
		t.Errorf("approved %v, sessions erased %d; want nothing erased", data.approved, f.ips.deletes-deletes)
	}
	if len(f.requests.reviews) != 1 || !strings.HasPrefix(f.requests.reviews[0], "rejected: Kept automatically") {
		t.Errorf("recorded decisions = %q; want the automatic refusal", f.requests.reviews)
	}

	// The person is told why, and the date their last session can go.
	noticed, err := f.svc.withLegalNotice(context.Background(), req)
	if err != nil || noticed.LegalNotice == nil {
		t.Fatalf("withLegalNotice = %+v, %v; want a notice", noticed.LegalNotice, err)
	}
	if want := f.svc.now().Add(2 * year); !noticed.LegalNotice.Until.Equal(want) ||
		noticed.LegalNotice.ReasonEN != legalFiveYears.ReasonEN || noticed.LegalNotice.ReasonNL != legalFiveYears.ReasonNL {
		t.Errorf("notice = %+v; want until %v with the org's reasons", noticed.LegalNotice, want)
	}
}

// The person's earlier data request is among the matches, held by no law, but
// it holds only who asked: the request is declined all the same, not sent to a
// reviewer who could erase nothing it asked for.
func TestEarlierDataRequestKeepsTheDecline(t *testing.T) {
	year := 365 * 24 * time.Hour
	f, data, _, earlier := legalFixture(t, year, year)
	delete(data.heldUntil, earlier.ID)
	data.matchKinds = map[uuid.UUID]FlowKind{earlier.ID: FlowDataErasure}

	req := f.reconcile(t)
	if req.Status != StatusRejected || req.ErrorCode != ErrorLegalRetention {
		t.Fatalf("request = %q %q; want rejected %s", req.Status, req.ErrorCode, ErrorLegalRetention)
	}
	if want := (DataHold{Sessions: 1, Until: f.svc.now().Add(year)}); data.held == nil || *data.held != want {
		t.Errorf("recorded hold = %+v; want %+v, the identity check alone", data.held, want)
	}
}

// An erasure that leaves an identity check the law holds tells the person what
// it kept, until when and why, whether a reviewer or the automatic handling
// approved it; once the hold passed, or for a refusal of the reviewer's own,
// it says nothing of the law.
func TestPartialErasureTellsWhatTheLawKeeps(t *testing.T) {
	year := 365 * 24 * time.Hour
	f, _, _, free := legalFixture(t, year, -time.Hour)
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("request = %q; want needs_review, a session is free", req.Status)
	}
	ctx, id := context.Background(), f.requests.stored.ID
	if _, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Approve: true, Reason: "ok", RequestIDs: []uuid.UUID{free.ID}}); err != nil {
		t.Fatalf("approving the free session: %v", err)
	}
	approved := *f.requests.stored
	until := f.svc.now().Add(year)
	if approved.Status != StatusApproved || approved.DataHold == nil || *approved.DataHold != (DataHold{Sessions: 1, Until: until}) {
		t.Fatalf("approved request = %q with hold %+v; want approved, one session held until %v", approved.Status, approved.DataHold, until)
	}
	noticed, err := f.svc.withLegalNotice(ctx, approved)
	if err != nil || noticed.LegalNotice == nil {
		t.Fatalf("withLegalNotice = %+v, %v; want a notice", noticed.LegalNotice, err)
	}
	if n := noticed.LegalNotice; n.Sessions != 1 || !n.Until.Equal(until) || n.ReasonEN != legalFiveYears.ReasonEN || n.ReasonNL != legalFiveYears.ReasonNL {
		t.Errorf("notice = %+v; want one session until %v with the org's reasons", n, until)
	}

	later := until.Add(time.Hour)
	f.svc.now = func() time.Time { return later }
	if noticed, err := f.svc.withLegalNotice(ctx, approved); err != nil || noticed.LegalNotice != nil {
		t.Errorf("withLegalNotice once the hold passed = %+v, %v; want none", noticed.LegalNotice, err)
	}

	refused := approved
	refused.Status, refused.ErrorCode = StatusRejected, ErrorReviewRejected
	f.svc.now = func() time.Time { return until.Add(-time.Hour) }
	if noticed, err := f.svc.withLegalNotice(ctx, refused); err != nil || noticed.LegalNotice != nil {
		t.Errorf("withLegalNotice of a reviewer's refusal = %+v, %v; want none", noticed.LegalNotice, err)
	}
}

// The org cannot erase through the API what its legal retention still holds,
// as no "delete my data" request can; once the period passed it can.
func TestPurgeRequestLegalHold(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent := f.sendExternal(t, "anna@example.org", "")
	until := time.Now().Add(time.Hour)
	f.requests.heldUntil = &until
	if err := f.svc.PurgeRequest(ctx, testOrg.ID, sent.Request.ID); !errors.Is(err, ErrLegalRetention) {
		t.Fatalf("PurgeRequest of a held check = %v; want ErrLegalRetention", err)
	}
	if f.ips.deletes != 0 || f.requests.stored.PurgedAt != nil {
		t.Fatalf("a refused purge erased %d sessions, purged at %v", f.ips.deletes, f.requests.stored.PurgedAt)
	}
	var apiErr *respond.APIError
	if !errors.As(mapError(ErrLegalRetention), &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "legal_retention" {
		t.Errorf("mapError(ErrLegalRetention) = %+v; want 409 legal_retention", apiErr)
	}

	passed := time.Now().Add(-time.Hour)
	f.requests.heldUntil = &passed
	if err := f.svc.PurgeRequest(ctx, testOrg.ID, sent.Request.ID); err != nil || f.ips.deletes != 1 || f.requests.stored.PurgedAt == nil {
		t.Errorf("PurgeRequest once the hold passed = %v (%d deletes); want it erased", err, f.ips.deletes)
	}
}

// A session the law no longer holds can be erased, so the request goes to a
// reviewer, who cannot take the held one.
func TestErasureReviewedIfAnyFree(t *testing.T) {
	year := 365 * 24 * time.Hour
	f, data, held, free := legalFixture(t, year, -time.Hour)

	req := f.reconcile(t)
	if req.Status != StatusNeedsReview {
		t.Fatalf("request = %q; want needs_review, a session is free", req.Status)
	}
	ctx, id := context.Background(), f.requests.stored.ID
	if _, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Approve: true, Reason: "ok", RequestIDs: []uuid.UUID{held.ID}}); !errors.Is(err, ErrLegalRetention) {
		t.Errorf("approving the held session = %v; want ErrLegalRetention", err)
	}
	if data.approved != nil {
		t.Fatalf("a refused decision recorded %v", data.approved)
	}
	if _, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Approve: true, Reason: "ok", RequestIDs: []uuid.UUID{free.ID}}); err != nil {
		t.Fatalf("approving the free session: %v", err)
	}
	if !slices.Equal(data.approved, []uuid.UUID{free.ID}) {
		t.Errorf("approved %v, want the free session", data.approved)
	}
}

// Without a legal retention nothing is held back: the request goes to review as
// it always did, and a declined one tells the person nothing about the law.
func TestErasureNotDeclinedNoLegal(t *testing.T) {
	f, _, _, _ := legalFixture(t, time.Hour, time.Hour)
	f.orgSettings.settings.LegalRetention = LegalRetention{}

	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("request = %q; want needs_review", req.Status)
	}
	declined := *f.requests.stored
	declined.ErrorCode = ErrorLegalRetention
	if noticed, err := f.svc.withLegalNotice(context.Background(), declined); err != nil || noticed.LegalNotice != nil {
		t.Errorf("withLegalNotice = %+v, %v; want none without a legal retention", noticed.LegalNotice, err)
	}
}

// Only an erasure is held back by the legal retention, so only its matches
// carry heldUntil: a reviewer can put a held session in a "see my data" export.
func TestDataMatchesHeldUntil(t *testing.T) {
	century := 100 * 365 * 24 * time.Hour
	for _, tt := range []struct {
		kind FlowKind
		held bool
	}{{FlowDataErasure, true}, {FlowDataAccess, false}} {
		t.Run(string(tt.kind), func(t *testing.T) {
			f, _, held, free := dataLegalFixture(t, tt.kind, century, -century)
			if req := f.reconcile(t); req.Status != StatusNeedsReview {
				t.Fatalf("request = %q; want needs_review", req.Status)
			}

			ctx := organization.ContextWithOrg(context.Background(), organization.Organization{ID: testOrg.ID})
			r := httptest.NewRequest(http.MethodGet, "/data-matches", nil).WithContext(ctx)
			r.SetPathValue("requestID", f.requests.stored.ID.String())
			rec := httptest.NewRecorder()
			if err := (&Handler{service: f.svc}).dataMatches(rec, r); err != nil {
				t.Fatalf("dataMatches: %v", err)
			}
			var body dataMatchesResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}

			got := map[string]bool{}
			for _, m := range body.Matches {
				got[m.RequestID] = m.HeldUntil != nil
			}
			if want := map[string]bool{held.ID.String(): tt.held, free.ID.String(): false}; !maps.Equal(got, want) {
				t.Errorf("matches with heldUntil = %v; want %v", got, want)
			}
		})
	}
}

// An automatic erasure erases the session the legal retention no longer holds
// and leaves the one it still holds: no erasure takes it before its time.
func TestAutomaticErasureLeavesHeldSessions(t *testing.T) {
	f, data := newDataFixture(FlowDataErasure)
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	f.orgSettings.settings.Session.ErasureHandling = ErasureAutomatic
	f.orgSettings.settings.LegalRetention = legalFiveYears
	held, free := heldSession("Anna Jansen"), heldSession("Anna Jansen")
	data.candidates = []Request{held, free}
	data.matched = []Request{held, free}
	data.checks = map[uuid.UUID]MatchLevel{held.ID: MatchStrong, free.ID: MatchStrong}
	data.heldUntil = map[uuid.UUID]time.Time{held.ID: now.Add(365 * 24 * time.Hour), free.ID: now.Add(-time.Hour)}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	deletes := f.ips.deletes

	req := f.reconcile(t)
	if req.Status != StatusApproved || !slices.Equal(data.approved, []uuid.UUID{free.ID}) {
		t.Errorf("status %q, approved %v; want approved with only the free session erased", req.Status, data.approved)
	}
	if got := f.ips.deletes - deletes; got != 2 {
		t.Errorf("sessions erased = %d; want the free session and the request", got)
	}
	if want := (DataHold{Sessions: 1, Until: now.Add(365 * 24 * time.Hour)}); f.requests.stored.DataHold == nil || *f.requests.stored.DataHold != want {
		t.Errorf("hold = %+v; want %+v for the person's page", f.requests.stored.DataHold, want)
	}
}

// The person's page gets what the law kept: how many sessions, until when, and
// the reason in both languages; nothing without a notice.
func TestHostedProgressLegalRetention(t *testing.T) {
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	until := now.AddDate(5, 0, 0)
	req := Request{FlowKind: FlowDataErasure, Status: StatusApproved, LegalNotice: &LegalNotice{
		Sessions: 2, Until: until, ReasonEN: legalFiveYears.ReasonEN, ReasonNL: legalFiveYears.ReasonNL,
	}}
	body, err := json.Marshal(newHostedProgress(req, now))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	type legalReason struct {
		EN string `json:"en"`
		NL string `json:"nl"`
	}
	var got struct {
		LegalRetention *struct {
			Sessions int         `json:"sessions"`
			Until    time.Time   `json:"until"`
			Reason   legalReason `json:"reason"`
		} `json:"legalRetention"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if l := got.LegalRetention; l == nil || l.Sessions != 2 || !l.Until.Equal(until) ||
		l.Reason.EN != legalFiveYears.ReasonEN || l.Reason.NL != legalFiveYears.ReasonNL {
		t.Errorf("legalRetention = %+v; want two sessions until %v with both reasons", l, until)
	}

	req.LegalNotice = nil
	if body, err := json.Marshal(newHostedProgress(req, now)); err != nil || strings.Contains(string(body), "legalRetention") {
		t.Errorf("progress without a notice = %s, %v; want no legalRetention", body, err)
	}
}
