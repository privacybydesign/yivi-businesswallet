package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// An admin reads an external request's identity in the wallet, audited each
// time; a member's request has no identity to show.
func TestAdminRequestResult(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, sam,
		NewRequest{SubjectEmail: "a@example.org", FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if _, _, err := f.svc.AdminRequestResult(ctx, testOrg.ID, sent.Request.ID); !errors.Is(err, ErrResultNotReady) {
		t.Errorf("AdminRequestResult while pending = %v, want %v", err, ErrResultNotReady)
	}
	f.requests.stored.Status = StatusApproved
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
	_, identity, err := f.svc.AdminRequestResult(ctx, testOrg.ID, sent.Request.ID)
	if err != nil || identity.FamilyName != "Jansen" || f.requests.resultReads != 1 {
		t.Fatalf("AdminRequestResult = %+v, %v (%d audited); want IPS's identity, audited", identity, err, f.requests.resultReads)
	}

	f.requests.stored.SubjectKind = SubjectMember
	if _, _, err := f.svc.AdminRequestResult(ctx, testOrg.ID, sent.Request.ID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("AdminRequestResult on a member's request = %v, want %v", err, ErrRequestNotFound)
	}
	if f.requests.resultReads != 1 {
		t.Errorf("a refused read was audited: %d reads, want 1", f.requests.resultReads)
	}
}

// An age_over flow's approval carries the age answer in the identity, and a
// rejection carries no identity at all.
func TestAPIResultAgeOver(t *testing.T) {
	req := Request{ID: uuid.New(), Status: StatusApproved}
	identity := proofingprovider.Identity{
		Result:  proofingprovider.Result{Status: proofingprovider.StatusApproved},
		AgeOver: &proofingprovider.AgeOver{Age: 18, Over: true},
	}
	out := newAPIResultResponse(req, identity, time.Now())
	if out.Identity == nil || out.Identity.AgeOver == nil || *out.Identity.AgeOver != (apiAgeOver{Age: 18, Over: true}) {
		t.Fatalf("identity = %+v, want ageOver 18, over", out.Identity)
	}
	if out.Identity.BirthDate != "" {
		t.Errorf("birthDate = %q, want none", out.Identity.BirthDate)
	}

	identity.Status = proofingprovider.StatusRejected
	if out := newAPIResultResponse(req, identity, time.Now()); out.Identity != nil {
		t.Errorf("rejected identity = %+v, want none", out.Identity)
	}

	// A check waiting for review shows its reviewer the same identity.
	if got := newAPIIdentity(identity); got.AgeOver == nil || *got.AgeOver != (apiAgeOver{Age: 18, Over: true}) {
		t.Errorf("identity in review = %+v, want ageOver 18, over", got)
	}
}
