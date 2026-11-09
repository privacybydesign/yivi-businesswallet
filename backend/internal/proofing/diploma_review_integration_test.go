//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowDiplomasAlwaysReviewSaved(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "acme")
	store := NewFlowDiplomaStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	if _, err := store.Save(ctx, orgID, "f1", DiplomasAlwaysReview); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if mode, err := store.Get(ctx, orgID, "f1"); err != nil || mode != DiplomasAlwaysReview {
		t.Errorf("Get = %q, %v; want always_review", mode, err)
	}
}

// On a request that reviews its diplomas, the kept extracts open one
// diplomas review item, which a reviewer decides as the step's own; under
// required none opens.
func TestDiplomasReviewOpensAndDecides(t *testing.T) {
	for _, tc := range []struct {
		mode DiplomaMode
		want ReviewState
	}{
		{DiplomasAlwaysReview, ReviewPending},
		{DiplomasRequired, ""},
	} {
		pool, _ := testdb.Fresh(t)
		requests := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
		diplomas := NewDiplomaStore(pool, audit.NewDBRecorder())
		orgID := makeOrg(t, pool, "acme")
		sam := makeUser(t, pool, "sam@example.org")
		rita := makeUser(t, pool, "rita@example.org")
		ctx := context.Background()
		in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
		in.Diplomas = tc.mode
		req := createStarted(t, requests, in, "s1")
		if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
			proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
			t.Fatalf("%s: RecordOutcome: %v", tc.mode, err)
		}
		for _, number := range []string{"2896311", "2896312"} {
			extract := Diploma{
				DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", Profiles: []string{},
				Institution: "Hogeschool Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
				DocumentNumber: number,
			}
			if _, added, err := diplomas.Add(ctx, req, extract); err != nil || !added {
				t.Fatalf("%s: Add %s = %v, %v; want added", tc.mode, number, added, err)
			}
		}

		got, err := requests.Get(ctx, orgID, req.ID)
		if err != nil || got.Status != StatusApproved || got.DiplomasReview != tc.want {
			t.Fatalf("%s: Get = %s, diplomas review %q, %v; want approved, %q", tc.mode, got.Status, got.DiplomasReview, err, tc.want)
		}
		waiting := 0
		if tc.want == ReviewPending {
			waiting = 1
		}
		counts, err := requests.ReviewCounts(ctx, orgID)
		if err != nil || counts.Kinds[ReviewKindDiplomas] != waiting || counts.StepSessions != waiting {
			t.Errorf("%s: ReviewCounts = %+v, %v; want %d waiting", tc.mode, counts, err, waiting)
		}
		if waiting == 0 {
			continue
		}
		decidedAt, err := requests.DecideStepReview(ctx, got, ReviewKindDiplomas, rita, ReviewRejected, "Not the asked qualification")
		if err != nil || decidedAt == nil {
			t.Fatalf("DecideStepReview = %v, %v; want decided", decidedAt, err)
		}
		decided, err := requests.Get(ctx, orgID, req.ID)
		steps := decided.Steps(time.Now())
		if err != nil || decided.Status != StatusApproved || decided.DiplomasReview != ReviewRejected ||
			len(steps) != 1 || steps[0].State != StepRejected || steps[0].Code != ErrorDiplomaRejected {
			t.Errorf("after rejecting = %s, %q, steps %+v, %v; want approved, the step rejected %s",
				decided.Status, decided.DiplomasReview, steps, err, ErrorDiplomaRejected)
		}
	}
}
