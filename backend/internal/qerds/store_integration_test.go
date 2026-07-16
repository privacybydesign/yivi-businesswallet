//go:build integration

package qerds_test

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// TestSelfSendReachesInbox is the regression for the provider_ref collision: a
// message sent to your own digital address loops back with the SAME provider ref
// as the outbound row. A global UNIQUE(provider_ref) silently dropped the inbound
// insert (ON CONFLICT DO NOTHING), leaving the inbox empty even though the sent
// item showed "delivered". Uniqueness must be per (direction, provider_ref).
func TestSelfSendReachesInbox(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', 'acme', 'kvk-acme', 'NL.KVK.acme', 'acme@qerds.localhost')`); err != nil {
		t.Fatalf("create org: %v", err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("get org: %v", err)
	}

	store := qerds.NewStore(pool, audit.NopRecorder{})
	const addr = "yivi@qerds.localhost"
	const ref = "stub-selfsend"
	ts := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

	out, err := store.CreateOutbound(ctx, org.ID, addr, addr, "hello", "world")
	if err != nil {
		t.Fatalf("CreateOutbound: %v", err)
	}
	if err := store.RecordSent(ctx, out.ID, qerdsprovider.SendReceipt{
		ProviderRef: ref,
		Status:      qerdsprovider.StatusDelivered,
		Evidence: []qerdsprovider.Evidence{
			{Type: qerdsprovider.EvidenceSubmissionAcceptance, ProviderRef: ref, QualifiedTimestamp: ts, Raw: []byte("{}")},
			{Type: qerdsprovider.EvidenceDelivery, ProviderRef: ref, QualifiedTimestamp: ts, Raw: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("RecordSent: %v", err)
	}

	inbound := qerdsprovider.InboundMessage{
		ProviderRef: ref,
		Sender:      addr,
		Recipient:   addr,
		Subject:     "hello",
		Body:        "world",
		Evidence: []qerdsprovider.Evidence{
			{Type: qerdsprovider.EvidenceDelivery, ProviderRef: ref, QualifiedTimestamp: ts, Raw: []byte("{}")},
		},
	}

	// First intake must store the inbound copy despite sharing the outbound ref.
	_, created, err := store.CreateInbound(ctx, org.ID, inbound)
	if err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	if !created {
		t.Fatal("inbound not stored — self-send collided on provider_ref")
	}

	// Repeated intake (poll/webhook retry) must dedupe.
	_, createdAgain, err := store.CreateInbound(ctx, org.ID, inbound)
	if err != nil {
		t.Fatalf("CreateInbound (again): %v", err)
	}
	if createdAgain {
		t.Fatal("duplicate inbound stored — dedupe on (direction, provider_ref) broken")
	}

	messages, err := store.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var inboundCount, outboundCount int
	for _, m := range messages {
		switch m.Direction {
		case qerds.DirectionInbound:
			inboundCount++
		case qerds.DirectionOutbound:
			outboundCount++
		}
	}
	if inboundCount != 1 || outboundCount != 1 {
		t.Fatalf("got inbound=%d outbound=%d, want 1 and 1", inboundCount, outboundCount)
	}
}
