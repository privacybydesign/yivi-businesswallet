//go:build integration

package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
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

// Two organizations in one wallet: the holder's decline marks the issuer's offer
// declined directly, and no message is sent for it.
func TestDeclineReachesAnIssuerInThisWalletWithoutAMessage(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, tmpl := rulebookTemplate(t, ctx, e, "lei")
	rb, _ := rulebook.Get("lei")

	var holderOrg uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Supplier', 'supplier', 'kvk-supplier', 'NLNHR.supplier', 'supplier@qerds.localhost') RETURNING id`).Scan(&holderOrg); err != nil {
		t.Fatalf("create holder org: %v", err)
	}
	for org, address := range map[uuid.UUID]string{e.orgID: "caesar@qerds.localhost", holderOrg: "supplier@qerds.localhost"} {
		if _, err := e.pool.Exec(ctx, `INSERT INTO qerds_addresses (organization_id, address, is_default) VALUES ($1, $2, true)`, org, address); err != nil {
			t.Fatalf("provision address %s: %v", address, err)
		}
	}

	result, err := e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: tmpl.ID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.localhost"},
		Attributes: rb.Sample(),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	sent := len(e.qerds.to)

	var messageID uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO qerds_messages
		(organization_id, direction, sender_address, recipient_address, subject, body, provider_ref, status)
		VALUES ($1, 'inbound', 'caesar@qerds.localhost', 'supplier@qerds.localhost', 'Credential offer', '', 'ref-local-decline', 'received')
		RETURNING id`, holderOrg).Scan(&messageID); err != nil {
		t.Fatalf("insert offer message: %v", err)
	}
	offer, _, err := e.store.RecordOffer(ctx, holderOrg, attestation.OfferInput{
		SourceMessageID: messageID, SenderOrgName: "Caesar", SenderAddress: "caesar@qerds.localhost",
		CredentialName: "LEI", Offer: result.OfferURI,
	})
	if err != nil {
		t.Fatalf("RecordOffer: %v", err)
	}

	e.service.SetLocalIssuers(qerds.NewStore(e.pool, audit.NopRecorder{}), e.store)
	if err := e.service.DeclineOffer(ctx, holderOrg, offer.ID); err != nil {
		t.Fatalf("DeclineOffer: %v", err)
	}

	issued, err := e.store.GetIssued(ctx, e.orgID, result.ID)
	if err != nil {
		t.Fatalf("GetIssued: %v", err)
	}
	if issued.Status != attestation.StatusDeclined {
		t.Fatalf("issuer's offer status = %q, want declined", issued.Status)
	}
	if len(e.qerds.to) != sent {
		t.Fatalf("declining sent a message: %v", e.qerds.to[sent:])
	}
}
