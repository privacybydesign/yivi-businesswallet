package proofing

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// fakeDataRequests holds flow kinds and one data request's matches.
type fakeDataRequests struct {
	kinds      map[string]FlowKind
	candidates []Request
	emailed    []Request
	matched    []Request
	matches    []DataMatch
	saved      []NewDataMatch
	approved   []uuid.UUID
	exportTill *time.Time
	exported   int
}

func (f *fakeDataRequests) FlowKind(_ context.Context, _ uuid.UUID, flowID string) (FlowKind, error) {
	if kind, ok := f.kinds[flowID]; ok {
		return kind, nil
	}
	return FlowIdentity, nil
}

func (f *fakeDataRequests) AllFlowKinds(context.Context, uuid.UUID) (map[string]FlowKind, error) {
	return f.kinds, nil
}

func (f *fakeDataRequests) SaveFlowKind(_ context.Context, _ uuid.UUID, flowID string, kind FlowKind) (FlowKind, error) {
	f.kinds[flowID] = kind
	return kind, nil
}

func (f *fakeDataRequests) Candidates(context.Context, Request) ([]Request, error) {
	return f.candidates, nil
}

func (f *fakeDataRequests) EmailCandidates(context.Context, Request) ([]Request, error) {
	return f.emailed, nil
}

func (f *fakeDataRequests) SaveMatches(_ context.Context, _ Request, matches []NewDataMatch) error {
	f.saved = matches
	f.matches = nil
	for _, m := range matches {
		f.matches = append(f.matches, DataMatch{RequestID: m.RequestID, Level: m.Level})
	}
	return nil
}

func (f *fakeDataRequests) Matches(context.Context, Request) ([]DataMatch, error) {
	return f.matches, nil
}

func (f *fakeDataRequests) MatchedRequests(context.Context, Request) ([]Request, error) {
	return f.matched, nil
}

func (f *fakeDataRequests) RecordDecision(_ context.Context, _ Request, approved []uuid.UUID, exportUntil *time.Time) error {
	f.approved, f.exportTill = approved, exportUntil
	return nil
}

func (f *fakeDataRequests) RecordExported(context.Context, Request, int) error {
	f.exported++
	return nil
}

// newDataFixture is newFixture with chipFlow made a data request flow of kind.
func newDataFixture(kind FlowKind) (fixture, *fakeDataRequests) {
	f := newFixture()
	data := &fakeDataRequests{kinds: map[string]FlowKind{chipFlow.ID: kind}}
	f.svc.dataRequests = data
	return f, data
}

// heldSession is a settled customer session holding personal data.
func heldSession(proofedName string) Request {
	return Request{
		ID: uuid.New(), OrganizationID: testOrg.ID, CustomerID: &initech.ID, Status: StatusApproved,
		ProofedName: proofedName, session: &ipsSession{ID: "s-" + uuid.NewString(), Token: "t"},
	}
}

// Every proofing route registers on one mux: net/http panics on two patterns
// that match the same path with neither more specific, which only the full
// server would otherwise hit.
func TestHandlerRoutesRegister(t *testing.T) {
	f, _ := newDataFixture(FlowDataErasure)
	pass := func(next http.Handler) http.Handler { return next }
	NewHandler(f.svc, pass, pass).Register(http.NewServeMux())
}

func TestMatchLevel(t *testing.T) {
	passport := &proofingprovider.Evidence{DocumentType: "P", IssuingState: "NLD", ExpiryDate: "2031-05-01"}
	requester := SubjectIdentity{
		GivenName: "Anna Maria", FamilyName: "Jansen", BirthDate: testBirthDate,
		DocumentType: "P", IssuingState: "NLD", ExpiryDate: "2031-05-01",
	}
	tests := []struct {
		name string
		held proofingprovider.Identity
		want MatchLevel
		ok   bool
	}{
		{"same document", proofingprovider.Identity{GivenName: "ANNA MARIA", FamilyName: "JANSEN", BirthDate: testBirthDate, Evidence: passport}, MatchStrong, true},
		{"another document", proofingprovider.Identity{
			GivenName: "Anna Maria", FamilyName: "Jansen", BirthDate: testBirthDate,
			Evidence: &proofingprovider.Evidence{DocumentType: "I", IssuingState: "NLD", ExpiryDate: "2029-01-01"},
		}, MatchProbable, true},
		{"no evidence", proofingprovider.Identity{GivenName: "Anna Maria", FamilyName: "Jansen", BirthDate: testBirthDate}, MatchProbable, true},
		{"born another day", proofingprovider.Identity{GivenName: "Anna Maria", FamilyName: "Jansen", BirthDate: "1990-04-13", Evidence: passport}, "", false},
		{"another person", proofingprovider.Identity{GivenName: "Piet", FamilyName: "de Vries", BirthDate: testBirthDate, Evidence: passport}, "", false},
		{"no birth date", proofingprovider.Identity{GivenName: "Anna Maria", FamilyName: "Jansen"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchLevel(requester, tt.held)
			if got != tt.want || ok != tt.ok {
				t.Errorf("matchLevel = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNameHolds(t *testing.T) {
	if !nameHolds("JANSEN, Ánna", "Jansen") || !nameHolds("Anna de Vries", "DE VRIES") {
		t.Error("nameHolds missed a family name in the name")
	}
	if nameHolds("Anna Pietersen", "Jansen") || nameHolds("Anna Vries", "de Vries") {
		t.Error("nameHolds took a name without the whole family name")
	}
}

// A proven person's data request is not approved: it goes to review with the
// customer's sessions of that person.
func TestDataRequestReviewMatches(t *testing.T) {
	f, data := newDataFixture(FlowDataErasure)
	anna, piet := heldSession("Anna Jansen"), heldSession("Piet de Vries")
	data.candidates = []Request{anna, piet}
	sent := f.sendForCustomer(t, "anna@example.org", "")
	if sent.Request.FlowKind != FlowDataErasure {
		t.Fatalf("sent kind = %q; want the flow's data_erasure", sent.Request.FlowKind)
	}
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}

	req := f.reconcile(t)
	if req.Status != StatusNeedsReview {
		t.Fatalf("status = %q; want needs_review", req.Status)
	}
	if len(data.saved) != 1 || data.saved[0].RequestID != anna.ID || data.saved[0].Level != MatchProbable {
		t.Errorf("matches = %+v; want only Anna's session, probable", data.saved)
	}
	if req.ProofedName != "Anna Jansen" {
		t.Errorf("proofed name = %q; want the name kept for the review", req.ProofedName)
	}
	// The engine keeps reporting approved: the review stays the wallet's.
	reads := f.ips.resultReads
	if again := f.reconcile(t); again.Status != StatusNeedsReview || f.ips.resultReads != reads {
		t.Errorf("after another read: %q, %d identity reads; want still in review, no new search", again.Status, f.ips.resultReads-reads)
	}
}

// An unfinished session sent to the request's address is matched by e-mail,
// after the identity matches; one already matched on identity is not listed
// twice.
func TestDataRequestMatchesByEmail(t *testing.T) {
	f, data := newDataFixture(FlowDataErasure)
	anna := heldSession("Anna Jansen")
	pending := Request{ID: uuid.New(), OrganizationID: testOrg.ID, CustomerID: &initech.ID, Status: StatusPending}
	data.candidates, data.emailed = []Request{anna}, []Request{pending, anna}
	f.sendForCustomer(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}

	f.reconcile(t)
	want := []NewDataMatch{{RequestID: anna.ID, Level: MatchProbable}, {RequestID: pending.ID, Level: MatchEmail}}
	if !slices.Equal(data.saved, want) {
		t.Errorf("matches = %+v; want %+v", data.saved, want)
	}
}

// A decision that names no sessions takes every identity match, never an
// e-mail one: those only a reviewer's own tick takes.
func TestApprovedSkipsEmailMatches(t *testing.T) {
	strong, email := uuid.New(), uuid.New()
	matches := []DataMatch{{RequestID: strong, Level: MatchStrong}, {RequestID: email, Level: MatchEmail}}
	got, err := approvedMatches(matches, nil)
	if err != nil || !slices.Equal(got, []uuid.UUID{strong}) {
		t.Errorf("approvedMatches(nil) = %v, %v; want only the identity match", got, err)
	}
	got, err = approvedMatches(matches, []uuid.UUID{email})
	if err != nil || !slices.Equal(got, []uuid.UUID{email}) {
		t.Errorf("approvedMatches(email) = %v, %v; want the ticked e-mail match", got, err)
	}
}

func TestDataRequestFlowCustomers(t *testing.T) {
	f, _ := newDataFixture(FlowDataAccess)
	f.settings.selection = FlowSelection{FlowIDs: []string{appFlow.ID, chipFlow.ID}, DefaultFlowID: appFlow.ID}
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New()},
		NewRequest{SubjectUserID: alex.UserID, FlowID: chipFlow.ID})
	if !errors.Is(err, ErrDataFlowForMember) {
		t.Errorf("member request on a data flow = %v; want ErrDataFlowForMember", err)
	}
	if err := f.svc.ConfigureFlows(context.Background(), testOrg, FlowSelection{FlowIDs: []string{chipFlow.ID}, DefaultFlowID: chipFlow.ID}); !errors.Is(err, ErrDataFlowForMember) {
		t.Errorf("offering a data flow to members = %v; want ErrDataFlowForMember", err)
	}
}

func TestSaveFlowKindNeedsIdentity(t *testing.T) {
	f, data := newDataFixture(FlowIdentity)
	ctx := context.Background()
	if _, err := f.svc.SaveFlowKind(ctx, testOrg, appFlow.ID, FlowDataErasure); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("appFlow (reads no identity) = %v; want ErrFlowNoIdentity", err)
	}
	f.settings.selection = FlowSelection{FlowIDs: []string{chipFlow.ID}, DefaultFlowID: chipFlow.ID}
	if _, err := f.svc.SaveFlowKind(ctx, testOrg, chipFlow.ID, FlowDataErasure); !errors.Is(err, ErrDataFlowForMember) {
		t.Errorf("a flow members may send = %v; want ErrDataFlowForMember", err)
	}
	f.settings.selection = FlowSelection{}
	if _, err := f.svc.SaveFlowKind(ctx, testOrg, chipFlow.ID, "everything"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown kind = %v; want ErrInvalidInput", err)
	}
	if kind, err := f.svc.SaveFlowKind(ctx, testOrg, chipFlow.ID, FlowDataAccess); err != nil || data.kinds[chipFlow.ID] != FlowDataAccess {
		t.Errorf("chipFlow = %q, %v; want data_access", kind, err)
	}
}

// inReview is a data request of kind awaiting review, matching two sessions.
func inReview(t *testing.T, kind FlowKind) (fixture, *fakeDataRequests, Request, Request) {
	t.Helper()
	f, data := newDataFixture(kind)
	first, second := heldSession("Anna Jansen"), heldSession("Anna Jansen")
	data.candidates = []Request{first, second}
	data.matched = []Request{first, second}
	f.sendForCustomer(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %q; want needs_review", req.Status)
	}
	return f, data, first, second
}

func TestDataErasurePurgesApproved(t *testing.T) {
	f, data, first, _ := inReview(t, FlowDataErasure)
	deletes := f.ips.deletes
	req, err := f.svc.DecideReview(context.Background(), testOrg.ID, f.requests.stored.ID, "reviewer@example.org",
		ReviewInput{Approve: true, Reason: "identity matches", RequestIDs: []uuid.UUID{first.ID}})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	if req.Status != StatusApproved || !slices.Equal(data.approved, []uuid.UUID{first.ID}) || data.exportTill != nil {
		t.Errorf("decision: %q, approved %v, export %v; want approved, the first match, no export", req.Status, data.approved, data.exportTill)
	}
	// The approved session and the request itself are erased; the other match stays.
	if got := f.ips.deletes - deletes; got != 2 {
		t.Errorf("sessions erased = %d; want 2", got)
	}
	// The engine had approved already: nothing is decided there.
	if len(f.ips.decisions) != 0 {
		t.Errorf("engine decisions = %d; want none", len(f.ips.decisions))
	}
}

// Approving without choosing takes only the matches proven with the same
// document: a probable or e-mail one needs the reviewer's own tick.
func TestApprovedTakesStrongOnly(t *testing.T) {
	strong, probable, email := uuid.New(), uuid.New(), uuid.New()
	matches := []DataMatch{{RequestID: strong, Level: MatchStrong}, {RequestID: probable, Level: MatchProbable}, {RequestID: email, Level: MatchEmail}}
	got, err := approvedMatches(matches, nil)
	if err != nil || !slices.Equal(got, []uuid.UUID{strong}) {
		t.Errorf("approvedMatches(nil) = %v, %v; want only the strong match", got, err)
	}
	if got, err := approvedMatches(matches, []uuid.UUID{probable, email}); err != nil || len(got) != 2 {
		t.Errorf("approvedMatches(ticked) = %v, %v; want both ticked", got, err)
	}
}

// Every given name and the family name must match, in order, with the birth
// date: a shared family name or one given name of two is someone else.
func TestSamePersonIsAFullMatch(t *testing.T) {
	for _, tc := range []struct {
		given, family, other string
		want                 bool
	}{
		{"Anna Maria", "Müller", "ANNA MARIA MUELLER", true},
		{"Anna", "Smit", "Anna Jansen Smit", false},
		{"Jan Willem", "de Vries", "Jan Pieter de Vries", false},
		{"Anna Maria", "Jansen", "Anna Jansen", false},
	} {
		if got := samePerson(tc.given, tc.family, testBirthDate, tc.other, testBirthDate); got != tc.want {
			t.Errorf("samePerson(%s %s, %s) = %v, want %v", tc.given, tc.family, tc.other, got, tc.want)
		}
	}
	if samePerson("Anna", "Jansen", testBirthDate, "Anna Jansen", "1990-04-13") {
		t.Error("another birth date matched")
	}
}

func TestDataAccessOpensExport(t *testing.T) {
	f, data, first, second := inReview(t, FlowDataAccess)
	deletes := f.ips.deletes
	req, err := f.svc.DecideReview(context.Background(), testOrg.ID, f.requests.stored.ID, "reviewer@example.org",
		ReviewInput{Approve: true, Reason: "identity matches", RequestIDs: []uuid.UUID{first.ID, second.ID}})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	if req.Status != StatusApproved || !slices.Equal(data.approved, []uuid.UUID{first.ID, second.ID}) || data.exportTill == nil ||
		data.exportTill.Sub(time.Now().Add(DataExportWindow)).Abs() > time.Minute {
		t.Errorf("decision: %q, approved %v, export %v; want the chosen matches approved, the export open", req.Status, data.approved, data.exportTill)
	}
	if f.ips.deletes != deletes {
		t.Errorf("an access request erased %d sessions; want none", f.ips.deletes-deletes)
	}
}

// An approved access request that is then erased exports nothing more,
// though its window is still open.
func TestErasedAccessExportsNothing(t *testing.T) {
	f, _, _, _ := inReview(t, FlowDataAccess)
	ctx := context.Background()
	req, err := f.svc.DecideReview(ctx, testOrg.ID, f.requests.stored.ID, "reviewer@example.org", ReviewInput{Approve: true, Reason: "ok"})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	until := time.Now().Add(DataExportWindow)
	f.requests.stored.DataExportUntil = &until
	if err := f.svc.PurgeRequest(ctx, initechScope, req.ID); err != nil {
		t.Fatalf("PurgeRequest: %v", err)
	}
	if _, err := f.svc.CustomerDataExport(ctx, initechScope, req.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Errorf("export after erasure = %v, want %v", err, ErrExportUnavailable)
	}
}

func TestDecideDataRequestRefusals(t *testing.T) {
	f, data, _, _ := inReview(t, FlowDataErasure)
	ctx := context.Background()
	id := f.requests.stored.ID
	if _, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Approve: true, Reason: "ok", RequestIDs: []uuid.UUID{uuid.New()}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("approving a session that did not match = %v; want ErrInvalidInput", err)
	}
	if data.approved != nil {
		t.Fatalf("a refused decision recorded %v; want nothing", data.approved)
	}
	req, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Reason: "Wwft: kept five years"})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if req.Status != StatusRejected || req.ErrorCode != ErrorReviewRejected || len(data.approved) != 0 {
		t.Errorf("rejection: %q %q, approved %v; want rejected, MANUAL_REVIEW_REJECTED, nothing approved", req.Status, req.ErrorCode, data.approved)
	}
	if _, err := f.svc.DecideReview(ctx, testOrg.ID, id, "r", ReviewInput{Approve: true, Reason: "again"}); !errors.Is(err, ErrNotUnderReview) {
		t.Errorf("deciding again = %v; want ErrNotUnderReview", err)
	}
}

// A data request nobody reviewed in time is rejected before it is purged,
// so it does not stay purged in review.
func TestPurgeRejectsLapsedReview(t *testing.T) {
	f, _, _, _ := inReview(t, FlowDataErasure)
	lapsed := time.Now().Add(-time.Hour)
	f.requests.stored.PurgeAt = &lapsed

	if purged, err := f.svc.PurgeDue(context.Background()); err != nil || purged != 1 {
		t.Fatalf("PurgeDue = %d, %v; want the lapsed request purged", purged, err)
	}
	if got := f.requests.stored; got.Status != StatusRejected || got.ErrorCode != errorReviewLapsed || got.PurgedAt == nil {
		t.Errorf("after PurgeDue = %q %q purged %v; want rejected %s and purged", got.Status, got.ErrorCode, got.PurgedAt, errorReviewLapsed)
	}
}

// Through the hosted link, which only its token guards, the data downloads for
// a day after the approval, not the whole DataExportWindow.
func TestHostedExportIsOpenADay(t *testing.T) {
	approved := time.Now()
	until := approved.Add(DataExportWindow)
	req := Request{Status: StatusApproved, DataExportUntil: &until}
	if got := openExport(req, approved.Add(hostedExportWindow-time.Minute)); got == nil {
		t.Error("closed within the first day")
	}
	if got := openExport(req, approved.Add(hostedExportWindow+time.Minute)); got != nil {
		t.Errorf("open until %v after the first day", got)
	}
}
