package qerdsprovider

import (
	"context"
	"testing"
	"time"
)

func TestStubProviderRoundTrip(t *testing.T) {
	fixed := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	p := NewStubProvider()
	p.now = func() time.Time { return fixed }

	ctx := context.Background()
	receipt, err := p.Send(ctx, OutboundMessage{
		Sender:    "alice@qerds.localhost",
		Recipient: "bob@qerds.localhost",
		Subject:   "hello",
		Body:      "world",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if receipt.ProviderRef == "" {
		t.Fatal("expected a provider ref")
	}
	if receipt.Status != StatusDelivered {
		t.Fatalf("status = %q, want %q", receipt.Status, StatusDelivered)
	}
	if len(receipt.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(receipt.Evidence))
	}
	if got := receipt.Evidence[0].Type; got != EvidenceSubmissionAcceptance {
		t.Fatalf("first evidence = %q, want %q", got, EvidenceSubmissionAcceptance)
	}
	if !receipt.Evidence[0].QualifiedTimestamp.Equal(fixed) {
		t.Fatalf("qualified timestamp = %v, want %v", receipt.Evidence[0].QualifiedTimestamp, fixed)
	}

	inbound, err := p.Fetch(ctx, "bob@qerds.localhost")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(inbound) != 1 {
		t.Fatalf("inbound count = %d, want 1", len(inbound))
	}
	if inbound[0].ProviderRef != receipt.ProviderRef {
		t.Fatalf("inbound ref = %q, want %q", inbound[0].ProviderRef, receipt.ProviderRef)
	}
	if inbound[0].Subject != "hello" || inbound[0].Body != "world" {
		t.Fatalf("inbound payload mismatch: %+v", inbound[0])
	}

	// Fetch drains the inbox.
	again, err := p.Fetch(ctx, "bob@qerds.localhost")
	if err != nil {
		t.Fatalf("Fetch (drain): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected empty inbox after drain, got %d", len(again))
	}
}
