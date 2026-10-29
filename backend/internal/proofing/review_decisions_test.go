package proofing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// listedEvents is an eventReader holding one request's events, newest first,
// as the audit store lists them.
type listedEvents []audit.Event

func (l listedEvents) ListForTarget(context.Context, uuid.UUID, string, string, *audit.Cursor, int) (audit.Page, error) {
	return audit.Page{Events: append([]audit.Event(nil), l...)}, nil
}

func TestRequestDecisionsReadsEachDecisionOldestFirst(t *testing.T) {
	sam := "Sam"
	early := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	svc := &Service{events: listedEvents{
		{
			ID: uuid.New(), Action: audit.IdentityProofingAddressReviewed, OccurredAt: late,
			Actor:    &audit.EventActor{GivenNames: "Rita", LastName: "de Vries"},
			Metadata: json.RawMessage(`{"before":{"addressReview":"pending"},"after":{"addressReview":"confirmed","reason":"moved in May"}}`),
		},
		{ID: uuid.New(), Action: audit.IdentityProofingResultRead, OccurredAt: early},
		{
			ID: uuid.New(), Action: audit.IdentityProofingReviewDecided, OccurredAt: early,
			Actor:    &audit.EventActor{PreferredName: &sam, GivenNames: "Samuel"},
			Metadata: json.RawMessage(`{"after":{"decision":"rejected","reason":"not the holder","errorCode":"REVIEW_NOT_HOLDER"}}`),
		},
	}}
	got, err := svc.RequestDecisions(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("RequestDecisions: %v", err)
	}
	want := []Decision{
		{Kind: DecisionReview, Outcome: "rejected", Reason: "not the holder", ErrorCode: "REVIEW_NOT_HOLDER", DecidedBy: "Sam", DecidedAt: early},
		{Kind: DecisionAddress, Outcome: "confirmed", Reason: "moved in May", DecidedBy: "Rita de Vries", DecidedAt: late},
	}
	if len(got) != len(want) {
		t.Fatalf("decisions = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("decision %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
