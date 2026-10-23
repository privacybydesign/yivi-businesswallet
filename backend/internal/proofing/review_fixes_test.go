package proofing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// Requiring diplomas on a flow that never reads the name and birth date
// would refuse every extract on its holder: it is refused when set.
func TestRequiredDiplomasNeedIdentity(t *testing.T) {
	f := newFixture()
	f.svc.flowDiplomaSettings = &fakeFlowDiplomas{modes: map[string]DiplomaMode{}}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, appFlow.ID, DiplomasRequired); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("required on a flow without identity = %v, want ErrFlowNoIdentity", err)
	}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, appFlow.ID, DiplomasOff); err != nil {
		t.Errorf("off on a flow without identity = %v, want saved", err)
	}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, chipFlow.ID, DiplomasRequired); err != nil {
		t.Errorf("required on a flow reading identity = %v, want saved", err)
	}
}

// A flow requiring diplomas keeps reading the name and birth date: an edit or
// an older version that drops dg1 is refused. A flow without that need is
// edited and rolled back freely.
func TestFlowEditsKeepTheIdentityRead(t *testing.T) {
	ctx := context.Background()
	noIdentity := proofingprovider.FlowSpec{Name: "Enrol", Steps: []string{"document_capture", "nfc_read"}, RequestedAttributes: []string{"dg2"}}
	withIdentity := noIdentity
	withIdentity.RequestedAttributes = []string{attributeDocument, "dg2"}

	d := newFixture()
	d.svc.flowDiplomaSettings = &fakeFlowDiplomas{modes: map[string]DiplomaMode{chipFlow.ID: DiplomasRequired}}
	if _, err := d.svc.EditFlow(ctx, testOrg, chipFlow.ID, noIdentity); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("diplomas-required flow edited without dg1 = %v, want ErrFlowNoIdentity", err)
	}
	if _, err := d.svc.EditFlow(ctx, testOrg, chipFlow.ID, withIdentity); err != nil {
		t.Errorf("diplomas-required flow edited with dg1 = %v, want saved", err)
	}
	// The fake's version 1 lists no attributes.
	if _, err := d.svc.ActivateFlowVersion(ctx, testOrg, chipFlow.ID, 1); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("diplomas-required flow back to a version without dg1 = %v, want ErrFlowNoIdentity", err)
	}
	if _, err := d.svc.EditFlow(ctx, testOrg, appFlow.ID, noIdentity); err != nil {
		t.Errorf("identity flow edited without dg1 = %v, want saved", err)
	}
	if _, err := d.svc.ActivateFlowVersion(ctx, testOrg, appFlow.ID, 1); err != nil {
		t.Errorf("identity flow back to version 1 = %v, want activated", err)
	}
}

// A member reads the timeline of a request they sent without the reviewer's
// reason, the subject's personal fields or which admin acted: the identity
// result is admin-only.
func TestMemberTimelineHidesAdminDetail(t *testing.T) {
	member, admin := uuid.New(), uuid.New()
	events := []audit.Event{
		{
			ID: uuid.New(), Action: audit.IdentityProofingRequested, Actor: &audit.EventActor{UserID: member},
			Metadata: json.RawMessage(`{"subjectName":"Anna Jansen","flowVersion":2}`),
		},
		{
			ID: uuid.New(), Action: audit.IdentityProofingReviewDecided, Actor: &audit.EventActor{UserID: admin},
			Metadata: json.RawMessage(`{"decision":"rejected","reason":"name on passport differs: J. Jansen"}`),
		},
		{ID: uuid.New(), Action: audit.IdentityProofingResultRead, Actor: &audit.EventActor{UserID: admin}},
	}
	if err := memberTimeline(events, member); err != nil {
		t.Fatalf("memberTimeline: %v", err)
	}
	if events[0].Actor == nil || string(events[0].Metadata) != `{"flowVersion":2}` {
		t.Errorf("own event = actor %v, metadata %s; want the member and no subject name", events[0].Actor, events[0].Metadata)
	}
	if string(events[1].Metadata) != `{"decision":"rejected"}` {
		t.Errorf("review metadata = %s, want the decision without its reason", events[1].Metadata)
	}
	for _, e := range events[1:] {
		if e.Actor != nil || e.ActorLabel == nil || *e.ActorLabel != adminActorLabel {
			t.Errorf("%s actor = %v label %v, want only %q", e.Action, e.Actor, e.ActorLabel, adminActorLabel)
		}
	}
}

// Without an encryption key, a keyed call gets the same documented 409 as an
// unkeyed one, not a 500.
func TestIdempotencyWithoutKeyIs409(t *testing.T) {
	h := &Handler{idempotency: NewIdempotencyStore(nil, nil)}
	r := httptest.NewRequest(http.MethodPost, "/proofing/sessions", strings.NewReader(`{}`))
	r.Header.Set(idempotencyHeader, "k-1")
	r = r.WithContext(context.WithValue(r.Context(), apiCallerKey{}, APIKeyCaller{Org: Org{ID: uuid.New()}}))
	w := httptest.NewRecorder()
	h.idempotent(func(http.ResponseWriter, *http.Request) error {
		t.Error("the call ran without a key to store its answer")
		return nil
	}).ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no_encryption_key") {
		t.Errorf("answer = %d %s, want 409 no_encryption_key", w.Code, w.Body.String())
	}
}

// A live link spends its org's hosted budget; once it is over it only spends
// its own, so an old link polled hard leaves the org's live pages alone.
func TestStaleHostedLinkSpendsItsOwnBudget(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	_, live, err := f.svc.hostedLimitKey(context.Background(), token)
	if err != nil {
		t.Fatalf("hostedLimitKey: %v", err)
	}
	if !live.live || live.key != testOrg.ID.String() {
		t.Errorf("waiting link's budget = %+v, want its org's", live)
	}
	if _, err := f.svc.DeclineHosted(context.Background(), token); err != nil {
		t.Fatalf("DeclineHosted: %v", err)
	}
	_, stale, err := f.svc.hostedLimitKey(context.Background(), token)
	if err != nil {
		t.Fatalf("hostedLimitKey: %v", err)
	}
	if stale.live || stale.key == testOrg.ID.String() {
		t.Errorf("declined link's budget = %+v, want its own", stale)
	}
}

// An ID card's MRZ cuts a long name off; the chip's DG11 holds it whole, and
// a request for that person matches it.
func TestExpectedSubjectMatchesTheChipsFullName(t *testing.T) {
	const fullName = "Johanna Wilhelmina Maria van den Berg-Schoonhoven"
	for name, tc := range map[string]struct {
		identity proofingprovider.Identity
		want     Status
	}{
		"DG11 has the whole name": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
			FullGivenName: "Johanna Wilhelmina Maria", FullFamilyName: "van den Berg-Schoonhoven",
		}, StatusApproved},
		"no DG11: the cut-off MRZ name": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
		}, StatusRejected},
		"DG11 names someone else": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
			FullGivenName: "Johanna", FullFamilyName: "van den Berg",
		}, StatusRejected},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			f.sendForPerson(t, fullName, testBirthDate)
			f.ips.identity = tc.identity
			f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
			if req := f.reconcile(t); req.Status != tc.want {
				t.Errorf("status = %s (%s), want %s", req.Status, req.ErrorCode, tc.want)
			}
		})
	}
}
