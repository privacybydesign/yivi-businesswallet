//go:build integration

package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
)

func TestDeclineFromRecipientMarksOfferDeclined(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, tmpl := rulebookTemplate(t, ctx, e, "lei")
	rb, _ := rulebook.Get("lei")
	result, err := e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: tmpl.ID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: rb.Sample(),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	receiver := attestation.NewDeclineReceiver(e.store)
	deliver := func(offerURI string) {
		t.Helper()
		body, err := attestation.MarshalOfferDeclinedEnvelope("Supplier", "LEI", offerURI)
		if err != nil {
			t.Fatalf("marshal decline: %v", err)
		}
		if err := receiver.OnInboundMessage(ctx, inbound(e.orgID, uuid.New(), "", "supplier@qerds.example", body)); err != nil {
			t.Fatalf("OnInboundMessage: %v", err)
		}
	}
	statusOf := func() string {
		t.Helper()
		row, err := e.store.GetIssued(ctx, e.orgID, result.ID)
		if err != nil {
			t.Fatalf("GetIssued: %v", err)
		}
		return row.Status
	}

	deliver("openid-credential-offer://some-other-offer")
	if got := statusOf(); got != attestation.StatusOffered {
		t.Fatalf("a decline of another offer changed this one to %q", got)
	}
	deliver(result.OfferURI)
	if got := statusOf(); got != attestation.StatusDeclined {
		t.Fatalf("status after the recipient declined = %q, want declined", got)
	}
	deliver(result.OfferURI)
	if got := statusOf(); got != attestation.StatusDeclined {
		t.Fatalf("a re-delivered decline changed the status to %q", got)
	}
}
