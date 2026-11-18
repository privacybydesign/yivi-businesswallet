package attestation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
)

func TestListIssuedMarksCollectedOfferClaimed(t *testing.T) {
	store := &fakeStore{row: Issued{ID: uuid.New(), Status: StatusOffered, IssuanceID: "iss-1", RecipientKind: RecipientOrganization}}
	iss := &fakeIssuer{status: openid4vciissuer.IssuanceStatus{Status: openid4vciissuer.StatusIssued, CredentialUUID: "cred-1"}}

	list, err := newRevokeService(store, iss).ListIssued(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListIssued: %v", err)
	}
	if len(list) != 1 || list[0].Status != StatusClaimed || list[0].CredentialUUID != "cred-1" {
		t.Fatalf("collected offer not claimed: %+v", list)
	}
}

func TestListIssuedLeavesPendingOfferOffered(t *testing.T) {
	store := &fakeStore{row: Issued{ID: uuid.New(), Status: StatusOffered, IssuanceID: "iss-1", RecipientKind: RecipientOrganization}}
	iss := &fakeIssuer{status: openid4vciissuer.IssuanceStatus{Status: openid4vciissuer.StatusPending}}

	list, err := newRevokeService(store, iss).ListIssued(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListIssued: %v", err)
	}
	if len(list) != 1 || list[0].Status != StatusOffered {
		t.Fatalf("pending offer changed: %+v", list)
	}
}

func TestListIssuedLeavesMemberOfferToItsOwnPolling(t *testing.T) {
	store := &fakeStore{row: Issued{ID: uuid.New(), Status: StatusOffered, IssuanceID: "iss-1", RecipientKind: RecipientMember}}
	iss := &fakeIssuer{status: openid4vciissuer.IssuanceStatus{Status: openid4vciissuer.StatusIssued, CredentialUUID: "cred-1"}}

	list, err := newRevokeService(store, iss).ListIssued(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListIssued: %v", err)
	}
	if len(list) != 1 || list[0].Status != StatusOffered {
		t.Fatalf("member offer reconciled by the list: %+v", list)
	}
}
