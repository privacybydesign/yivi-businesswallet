//go:build integration

package qerds_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

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

	out, err := store.CreateOutbound(ctx, org.ID, addr, addr, "hello", "world", []qerdsprovider.Attachment{
		{Filename: "filing.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4 stub")},
	})
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
	first, created, err := store.CreateInbound(ctx, org.ID, inbound)
	if err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	if !created {
		t.Fatal("inbound not stored — self-send collided on provider_ref")
	}

	// Repeated intake (poll/webhook retry) must dedupe, but still return the
	// existing row so the inbound consumer can re-run idempotently on re-delivery.
	again, createdAgain, err := store.CreateInbound(ctx, org.ID, inbound)
	if err != nil {
		t.Fatalf("CreateInbound (again): %v", err)
	}
	if createdAgain {
		t.Fatal("duplicate inbound stored — dedupe on (direction, provider_ref) broken")
	}
	if again.ID != first.ID {
		t.Fatalf("dedupe hit returned id %s, want the existing row %s", again.ID, first.ID)
	}
	if again.Body != inbound.Body {
		t.Fatalf("dedupe hit returned empty body %q, want %q — consumer would re-run with no offer", again.Body, inbound.Body)
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

// TestAttachmentPersistenceAndScoping covers attachment storage: the outbound
// message carries its payload metadata, the bytes are downloadable, and the
// download is scoped to the owning organization.
func TestAttachmentPersistenceAndScoping(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	orgID := seedOrg(t, ctx, pool, "acme")
	otherID := seedOrg(t, ctx, pool, "other")

	store := qerds.NewStore(pool, audit.NopRecorder{})
	const addr = "acme@qerds.localhost"
	content := []byte("%PDF-1.4 quarterly filing")
	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])

	out, err := store.CreateOutbound(ctx, orgID, addr, "bob@qerds.localhost", "filing", "see attached", []qerdsprovider.Attachment{
		{Filename: "filing.pdf", ContentType: "application/pdf", Content: content},
	})
	if err != nil {
		t.Fatalf("CreateOutbound: %v", err)
	}

	detail, err := store.GetWithEvidence(ctx, orgID, out.ID)
	if err != nil {
		t.Fatalf("GetWithEvidence: %v", err)
	}
	if len(detail.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.Filename != "filing.pdf" || att.ContentType != "application/pdf" {
		t.Errorf("attachment metadata = %+v", att)
	}
	if att.ContentHash != wantHash {
		t.Errorf("content hash = %q, want %q", att.ContentHash, wantHash)
	}
	if att.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", att.SizeBytes, len(content))
	}

	got, err := store.GetAttachmentContent(ctx, orgID, out.ID, att.ID)
	if err != nil {
		t.Fatalf("GetAttachmentContent: %v", err)
	}
	if !bytes.Equal(got.Content, content) {
		t.Errorf("downloaded content mismatch")
	}
	if got.Filename != "filing.pdf" || got.ContentType != "application/pdf" {
		t.Errorf("download metadata = %+v", got)
	}

	// Another organization must not be able to read the payload by id.
	if _, err := store.GetAttachmentContent(ctx, otherID, out.ID, att.ID); !errors.Is(err, qerds.ErrAttachmentNotFound) {
		t.Fatalf("cross-org download err = %v, want ErrAttachmentNotFound", err)
	}

	// An unknown attachment id is a not-found, not a server error.
	if _, err := store.GetAttachmentContent(ctx, orgID, out.ID, uuid.New()); !errors.Is(err, qerds.ErrAttachmentNotFound) {
		t.Fatalf("unknown attachment err = %v, want ErrAttachmentNotFound", err)
	}
}
