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
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// fakeDataRequests holds flow kinds and one data request's matches.
type fakeDataRequests struct {
	kinds      map[string]FlowKind
	candidates []Request
	unfinished []Request
	matched    []Request
	matches    []DataMatch
	saved      []NewDataMatch
	approved   []uuid.UUID
	exportTill *time.Time
	kept       *int
	exported   int
	// checks are the candidates read per data request; matchingBusy makes
	// TryLockMatching find the lock held; saveErr fails SaveMatches.
	checks       map[uuid.UUID]MatchLevel
	identityKeys map[uuid.UUID]SubjectIdentity
	matchingBusy bool
	saveErr      error
	// heldUntil is when the legal retention lets a matched session be erased;
	// SaveMatches stamps it on the matches it stores.
	heldUntil map[uuid.UUID]time.Time
	// matchKinds is the flow kind of a matched session, FlowIdentity when
	// unset; SaveMatches stamps it on the matches it stores.
	matchKinds map[uuid.UUID]FlowKind
	// held is what the last decision recorded the legal retention holds;
	// requests, when set, gets it and kept on its stored request too.
	held     *DataHold
	requests *fakeRequests
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

func (f *fakeDataRequests) Candidates(context.Context, Request, SubjectIdentity) ([]Request, error) {
	return f.candidates, nil
}

func (f *fakeDataRequests) RecordIdentityKey(_ context.Context, req Request, id SubjectIdentity) error {
	if f.identityKeys == nil {
		f.identityKeys = map[uuid.UUID]SubjectIdentity{}
	}
	f.identityKeys[req.ID] = id
	return nil
}

func (f *fakeDataRequests) UnfinishedCandidates(context.Context, Request) ([]Request, error) {
	return f.unfinished, nil
}

func (f *fakeDataRequests) TryLockMatching(context.Context, uuid.UUID) (func(), bool, error) {
	if f.matchingBusy {
		return nil, false, nil
	}
	return func() {}, true, nil
}

func (f *fakeDataRequests) MatchChecks(context.Context, Request) (map[uuid.UUID]MatchLevel, error) {
	return maps.Clone(f.checks), nil
}

func (f *fakeDataRequests) RecordMatchCheck(_ context.Context, _ Request, candidateID uuid.UUID, level MatchLevel) error {
	if f.checks == nil {
		f.checks = map[uuid.UUID]MatchLevel{}
	}
	if _, ok := f.checks[candidateID]; !ok {
		f.checks[candidateID] = level
	}
	return nil
}

func (f *fakeDataRequests) SaveMatches(_ context.Context, _ Request, matches []NewDataMatch) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = matches
	f.matches = nil
	for _, m := range matches {
		match := DataMatch{RequestID: m.RequestID, Level: m.Level, FlowKind: cmp.Or(f.matchKinds[m.RequestID], FlowIdentity)}
		if until, ok := f.heldUntil[m.RequestID]; ok {
			match.HeldUntil = &until
		}
		f.matches = append(f.matches, match)
	}
	return nil
}

func (f *fakeDataRequests) Matches(context.Context, Request) ([]DataMatch, error) {
	return f.matches, nil
}

func (f *fakeDataRequests) MatchedRequests(context.Context, Request) ([]Request, error) {
	return f.matched, nil
}

func (f *fakeDataRequests) RecordDecision(_ context.Context, req Request, approved []uuid.UUID, exportUntil *time.Time, kept *int, held *DataHold) error {
	f.approved, f.exportTill, f.kept, f.held = approved, exportUntil, kept, held
	if f.requests != nil && f.requests.stored != nil && f.requests.stored.ID == req.ID {
		f.requests.stored.DataMatchesKept, f.requests.stored.DataHold = kept, held
	}
	return nil
}

func (f *fakeDataRequests) RecordExported(context.Context, Request, int) error {
	f.exported++
	return nil
}

// newDataFixture is newFixture with chipFlow made a data request flow of kind.
func newDataFixture(kind FlowKind) (fixture, *fakeDataRequests) {
	f := newFixture()
	data := &fakeDataRequests{kinds: map[string]FlowKind{chipFlow.ID: kind}, requests: f.requests}
	f.svc.dataRequests = data
	return f, data
}

// heldSession is a settled external session holding personal data.
func heldSession(proofedName string) Request {
	return Request{
		ID: uuid.New(), OrganizationID: testOrg.ID, SubjectKind: SubjectExternal, Status: StatusApproved,
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

// A proven person's data request is not approved: it goes to review with the
// org's external sessions of that person.
func TestDataRequestReviewMatches(t *testing.T) {
	f, data := newDataFixture(FlowDataErasure)
	// The store gives only the sessions with the requester's identity match key.
	anna := heldSession("Anna Jansen")
	data.candidates = []Request{anna}
	sent := f.sendExternal(t, "anna@example.org", "")
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
	pending := Request{ID: uuid.New(), OrganizationID: testOrg.ID, SubjectKind: SubjectExternal, Status: StatusPending, SubjectEmail: "Anna@Example.org"}
	other := Request{ID: uuid.New(), OrganizationID: testOrg.ID, SubjectKind: SubjectExternal, Status: StatusPending, SubjectEmail: "piet@example.org"}
	data.candidates, data.unfinished = []Request{anna}, []Request{pending, other, anna}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}

	f.reconcile(t)
	want := []NewDataMatch{{RequestID: anna.ID, Level: MatchProbable}, {RequestID: pending.ID, Level: MatchEmail}}
	if !slices.Equal(data.saved, want) {
		t.Errorf("matches = %+v; want %+v", data.saved, want)
	}
}

// An unfinished session sent without the request's address is matched by its
// typed name when that is the requester's full name, case aside; a partial or
// other name is not.
func TestDataRequestMatchesByName(t *testing.T) {
	f, data := newDataFixture(FlowDataErasure)
	unfinished := func(name string) Request {
		return Request{ID: uuid.New(), OrganizationID: testOrg.ID, SubjectKind: SubjectExternal, Status: StatusPending, SubjectName: name}
	}
	named, partial, other, unnamed := unfinished("ANNA JANSEN"), unfinished("Jansen"), unfinished("Piet Jansen"), unfinished("")
	data.unfinished = []Request{named, partial, other, unnamed}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}

	f.reconcile(t)
	want := []NewDataMatch{{RequestID: named.ID, Level: MatchName}}
	if !slices.Equal(data.saved, want) {
		t.Errorf("matches = %+v; want %+v", data.saved, want)
	}
}

// A decision that names no sessions takes every identity match, never an
// e-mail one: those only a reviewer's own tick takes.
func TestApprovedSkipsEmailMatches(t *testing.T) {
	strong, email, name := uuid.New(), uuid.New(), uuid.New()
	matches := []DataMatch{{RequestID: strong, Level: MatchStrong}, {RequestID: email, Level: MatchEmail}, {RequestID: name, Level: MatchName}}
	got, err := approvedMatches(matches, nil, FlowDataAccess, time.Time{})
	if err != nil || !slices.Equal(got, []uuid.UUID{strong}) {
		t.Errorf("approvedMatches(nil) = %v, %v; want only the identity match", got, err)
	}
	got, err = approvedMatches(matches, []uuid.UUID{email}, FlowDataAccess, time.Time{})
	if err != nil || !slices.Equal(got, []uuid.UUID{email}) {
		t.Errorf("approvedMatches(email) = %v, %v; want the ticked e-mail match", got, err)
	}
}

func TestDataFlowsNotForMembers(t *testing.T) {
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
	if err := f.svc.ConfigureFlows(context.Background(), testOrg, FlowSelection{DefaultFlowID: chipFlow.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a data flow as the default = %v; want ErrInvalidInput", err)
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
	f.settings.selection = FlowSelection{DefaultFlowID: chipFlow.ID}
	if _, err := f.svc.SaveFlowKind(ctx, testOrg, chipFlow.ID, FlowDataErasure); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("the default flow = %v; want ErrInvalidInput", err)
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
	// The first was proven with the requester's document, the second only
	// shares the name and date of birth.
	first, second := heldSession("Anna Jansen"), heldSession("Anna Jansen")
	data.candidates = []Request{first, second}
	data.matched = []Request{first, second}
	data.checks = map[uuid.UUID]MatchLevel{first.ID: MatchStrong}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %q; want needs_review", req.Status)
	}
	return f, data, first, second
}

// A probable match is erased like a strong one: the same name and date of
// birth were read off a verified chip, only from another document.
func TestDataErasureTakesProbable(t *testing.T) {
	f, data, first, second := inReview(t, FlowDataErasure)
	deletes := f.ips.deletes
	req, err := f.svc.DecideReview(context.Background(), testOrg.ID, f.requests.stored.ID, "reviewer@example.org",
		ReviewInput{Approve: true, Reason: "her ID card and her passport", RequestIDs: []uuid.UUID{first.ID, second.ID}})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	if req.Status != StatusApproved || len(data.approved) != 2 || data.kept == nil || *data.kept != 0 {
		t.Errorf("decision: %q, approved %v, kept %v; want both erased, none kept", req.Status, data.approved, data.kept)
	}
	// Both matches and the request itself.
	if got := f.ips.deletes - deletes; got != 3 {
		t.Errorf("sessions erased = %d; want 3", got)
	}
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
	if data.kept == nil || *data.kept != 1 {
		t.Errorf("kept = %v; want the unticked match", data.kept)
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

// Approving without choosing takes the sure matches for an erasure (the same
// name and date of birth off a verified chip) and only those proven with the
// same document for "see my data". Anything else needs the reviewer's tick,
// which an erasure takes too. A match an automatic erasure purged is approved
// whatever was ticked.
func TestApprovedTakesSureMatches(t *testing.T) {
	strong, probable, email := uuid.New(), uuid.New(), uuid.New()
	matches := []DataMatch{{RequestID: strong, Level: MatchStrong}, {RequestID: probable, Level: MatchProbable}, {RequestID: email, Level: MatchEmail}}
	if got, err := approvedMatches(matches, nil, FlowDataAccess, time.Time{}); err != nil || !slices.Equal(got, []uuid.UUID{strong}) {
		t.Errorf("see my data approvedMatches(nil) = %v, %v; want the strong match", got, err)
	}
	if got, err := approvedMatches(matches, nil, FlowDataErasure, time.Time{}); err != nil || !slices.Equal(got, []uuid.UUID{strong, probable}) {
		t.Errorf("erasure approvedMatches(nil) = %v, %v; want the strong and the probable match", got, err)
	}
	if got, err := approvedMatches(matches, []uuid.UUID{email}, FlowDataErasure, time.Time{}); err != nil || !slices.Equal(got, []uuid.UUID{email}) {
		t.Errorf("erasure ticking the e-mail match = %v, %v; want it taken", got, err)
	}
	now := time.Now()
	erased := []DataMatch{{RequestID: strong, Level: MatchStrong, PurgedAt: &now}, {RequestID: email, Level: MatchEmail}}
	if got, err := approvedMatches(erased, []uuid.UUID{}, FlowDataErasure, time.Time{}); err != nil || !slices.Equal(got, []uuid.UUID{strong}) {
		t.Errorf("erasure ticking nothing after an automatic erasure = %v, %v; want the erased match", got, err)
	}
	if kept := keptMatches(erased, []uuid.UUID{strong}); kept != 1 {
		t.Errorf("kept = %d; want the unticked e-mail match", kept)
	}
}

// erasureInReview is a "delete my data" request of an org whose erasures are
// decided as handling says, proven, matching a strong and a probable
// session, and with unsure an unfinished one sent to the same address.
func erasureInReview(t *testing.T, handling ErasureHandling, unsure bool) (fixture, *fakeDataRequests, Request) {
	t.Helper()
	f, data := newDataFixture(FlowDataErasure)
	f.orgSettings.settings.Session.ErasureHandling = handling
	first, second := heldSession("Anna Jansen"), heldSession("Anna Jansen")
	data.candidates = []Request{first, second}
	data.matched = []Request{first, second}
	data.checks = map[uuid.UUID]MatchLevel{first.ID: MatchStrong}
	if unsure {
		data.unfinished = []Request{{ID: uuid.New(), SubjectEmail: "anna@example.org", Status: StatusPending}}
	}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	return f, data, f.reconcile(t)
}

// An org can have its erasures decided without a reviewer: erased at once
// when every match is sure, the unsure ones left to a reviewer.
func TestErasureHandling(t *testing.T) {
	t.Run("automatic, all sure", func(t *testing.T) {
		f, data, req := erasureInReview(t, ErasureAutomatic, false)
		if req.Status != StatusApproved || len(data.approved) != 2 {
			t.Errorf("status %q, approved %v; want approved with both matches erased", req.Status, data.approved)
		}
		if got := f.ips.deletes; got != 3 {
			t.Errorf("sessions erased = %d; want both matches and the request", got)
		}
	})
	t.Run("automatic, one unsure", func(t *testing.T) {
		f, data, req := erasureInReview(t, ErasureAutomatic, true)
		if req.Status != StatusNeedsReview || data.approved != nil {
			t.Errorf("status %q, decided %v; want it left in review", req.Status, data.approved)
		}
		if got := f.ips.deletes; got != 2 {
			t.Errorf("sessions erased = %d; want the two sure matches now", got)
		}
	})
	t.Run("keep is gone", func(t *testing.T) {
		// What the law makes an org keep is its legal retention (D6).
		f := newFixture()
		keep := ErasureHandling("keep")
		if _, err := f.svc.UpdateSettings(context.Background(), testOrg.ID, SettingsChange{ErasureHandling: &keep}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("UpdateSettings(keep) = %v; want ErrInvalidInput", err)
		}
		if got := f.orgSettings.settings.Session.ErasureHandling; got != ErasureReview {
			t.Errorf("erasure handling = %q; want it left at review", got)
		}
	})
	t.Run("review", func(t *testing.T) {
		f, _, req := erasureInReview(t, ErasureReview, false)
		if req.Status != StatusNeedsReview || f.ips.deletes != 0 {
			t.Errorf("status %q, erased %d; want it waiting for a reviewer", req.Status, f.ips.deletes)
		}
	})
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
	if err := f.svc.PurgeRequest(ctx, testOrg.ID, req.ID); err != nil {
		t.Fatalf("PurgeRequest: %v", err)
	}
	if _, err := f.svc.ExternalDataExport(ctx, testOrg.ID, req.ID); !errors.Is(err, ErrExportUnavailable) {
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

// An approved "see my data" export carries personal data for a strong match
// only: a ticked probable match may be someone else's session.
func TestExportPersonalOnlyStrong(t *testing.T) {
	f, data, first, second := inReview(t, FlowDataAccess)
	ctx := context.Background()
	data.matches = []DataMatch{{RequestID: first.ID, Level: MatchStrong}, {RequestID: second.ID, Level: MatchProbable}}
	req, err := f.svc.DecideReview(ctx, testOrg.ID, f.requests.stored.ID, "reviewer@example.org",
		ReviewInput{Approve: true, Reason: "ok", RequestIDs: []uuid.UUID{first.ID, second.ID}})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	yes := true
	data.matches[0].Approved, data.matches[1].Approved = &yes, &yes
	f.requests.stored.DataExportUntil = data.exportTill

	export, err := f.svc.AdminDataExport(ctx, testOrg.ID, req.ID)
	if err != nil {
		t.Fatalf("AdminDataExport: %v", err)
	}
	if len(export.Sessions) != 2 {
		t.Fatalf("exported %d sessions, want 2", len(export.Sessions))
	}
	for _, s := range export.Sessions {
		strong := s.Request.ID == first.ID
		if s.Personal != strong || (s.Identity != nil) != strong {
			t.Errorf("session %s (%s): personal %v, identity %v; want personal data only for the strong match",
				s.Request.ID, s.Level, s.Personal, s.Identity != nil)
		}
	}
}

// While one reconcile finds a data request's matches, another (a hosted
// status poll meanwhile, on any replica) does not read the candidates from the
// engine again.
func TestMatchingRunsOnce(t *testing.T) {
	f, data := newDataFixture(FlowDataAccess)
	data.candidates = []Request{heldSession("Anna Jansen")}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	data.matchingBusy = true

	reads := f.ips.identityReads
	if req := f.reconcile(t); req.Status == StatusNeedsReview {
		t.Errorf("status = %q while another reconcile is matching; want it left to that one", req.Status)
	}
	if f.ips.identityReads != reads || data.saved != nil {
		t.Errorf("read %d identities and saved %v; want neither", f.ips.identityReads-reads, data.saved)
	}

	data.matchingBusy = false
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Errorf("status = %q once free; want needs_review", req.Status)
	}
}

// A candidate is read from the engine once: a search that failed after it
// (here saving the matches) reads only the requester again on its retry,
// candidates without a kept proofed name included.
func TestCandidateReadOnce(t *testing.T) {
	f, data := newDataFixture(FlowDataAccess)
	first, second := heldSession(""), heldSession("")
	data.candidates = []Request{first, second}
	f.sendExternal(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial, Name: "Anna Jansen"}
	data.saveErr = errors.New("database down")

	reads := f.ips.identityReads
	if req := f.reconcile(t); req.Status == StatusNeedsReview {
		t.Fatalf("status = %q after a failed search; want it retried", req.Status)
	}
	if got := f.ips.identityReads - reads; got != 3 {
		t.Fatalf("first search read %d identities; want the requester and both candidates", got)
	}

	data.saveErr = nil
	reads = f.ips.identityReads
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %q on the retry; want needs_review", req.Status)
	}
	if got := f.ips.identityReads - reads; got != 1 {
		t.Errorf("retry read %d identities; want only the requester's", got)
	}
	want := []NewDataMatch{{RequestID: first.ID, Level: MatchProbable}, {RequestID: second.ID, Level: MatchProbable}}
	if !slices.Equal(data.saved, want) {
		t.Errorf("matches = %+v; want %+v from the kept checks", data.saved, want)
	}
}

// Bob proofs on the link an org sent Alice, then asks for his own data:
// his session matches strongly (it holds his identity), but it was never
// shown to be Alice's, so the export carries his identity and not the name
// and address the sender typed for Alice. A session approved for the one
// person it was sent to does carry them. (The fake engine proofs everyone as
// Anna Jansen: Bob, here.)
func TestExportContactOnlyWhenAddressed(t *testing.T) {
	f, data, alicesLink, addressed := inReview(t, FlowDataAccess)
	ctx := context.Background()
	alicesLink.SubjectName, alicesLink.SubjectEmail = "Alice de Boer", "alice@example.org"
	addressed.SubjectName, addressed.SubjectEmail, addressed.ExpectsSubject = "Anna Jansen", "anna@example.org", true
	data.matched = []Request{alicesLink, addressed}
	data.matches = []DataMatch{{RequestID: alicesLink.ID, Level: MatchStrong}, {RequestID: addressed.ID, Level: MatchStrong}}
	req, err := f.svc.DecideReview(ctx, testOrg.ID, f.requests.stored.ID, "reviewer@example.org",
		ReviewInput{Approve: true, Reason: "ok"})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	yes := true
	data.matches[0].Approved, data.matches[1].Approved = &yes, &yes
	f.requests.stored.DataExportUntil = data.exportTill

	export, err := f.svc.AdminDataExport(ctx, testOrg.ID, req.ID)
	if err != nil {
		t.Fatalf("AdminDataExport: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := writeDataExport(rec, httptest.NewRequest(http.MethodGet, "/", nil), export, nil); err != nil {
		t.Fatalf("writeDataExport: %v", err)
	}
	var out dataExportResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	contacts := map[string]*dataExportContact{}
	for _, s := range out.Sessions {
		if s.Identity == nil {
			t.Errorf("session %s: no identity; want the strong match's", s.SessionID)
		}
		contacts[s.SessionID] = s.Contact
	}
	if c := contacts[publicSessionID(alicesLink.ID)]; c != nil {
		t.Errorf("Alice's link exported contact %+v; want none", c)
	}
	if c := contacts[publicSessionID(addressed.ID)]; c == nil || c.Email != "anna@example.org" {
		t.Errorf("addressed session exported contact %+v; want anna@example.org", c)
	}
}

// An external person's session keeps who it proofed as an identity match key
// once it is decided, rejected included; a member's session does not.
func TestOutcomeRecordsIdentityKey(t *testing.T) {
	tests := []struct {
		name     string
		external bool
		result   proofingprovider.Result
		want     bool
	}{
		{"approved", true, proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}, true},
		{"rejected", true, proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: "FACE_MISMATCH"}, true},
		{"member", false, proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, data := newDataFixture(FlowIdentity)
			if tt.external {
				f.sendExternal(t, "anna@example.org", "")
			} else {
				f.send(t)
			}
			f.ips.result = tt.result
			req := f.reconcile(t)
			id, ok := data.identityKeys[req.ID]
			if ok != tt.want || (ok && (id.FamilyName != "Jansen" || id.BirthDate != testBirthDate)) {
				t.Errorf("identity key = %+v, %v; want recorded %v", id, ok, tt.want)
			}
		})
	}
}
