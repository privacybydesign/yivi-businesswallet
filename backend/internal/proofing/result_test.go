package proofing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// An admin reads a customer request's identity in the wallet, audited each
// time; a member's request has no identity to show.
func TestAdminRequestResult(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()},
		NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID})
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

	f.requests.stored.CustomerID = nil
	if _, _, err := f.svc.AdminRequestResult(ctx, testOrg.ID, sent.Request.ID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("AdminRequestResult on a member's request = %v, want %v", err, ErrRequestNotFound)
	}
	if f.requests.resultReads != 1 {
		t.Errorf("a refused read was audited: %d reads, want 1", f.requests.resultReads)
	}
}
