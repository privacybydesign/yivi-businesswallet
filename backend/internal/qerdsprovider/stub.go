package qerdsprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// StubProvider is an in-process QERDS provider for local dev and tests. Send
// performs a real round-trip: it mints ERDS-style evidence with qualified
// timestamps and loops the message into the recipient's inbox, so a Fetch on
// the receiving org returns it. This exercises the whole send/receive/evidence
// flow offline.
//
// It proves plumbing, NOT compliance — the qualified evidence and timestamps
// are only truly exercised against a real QTSP sandbox. See .ai/features/qerds.md.
type StubProvider struct {
	mu    sync.Mutex
	seq   int
	inbox map[Address][]InboundMessage
	now   func() time.Time
}

// NewStubProvider returns a StubProvider using the wall clock for timestamps.
func NewStubProvider() *StubProvider {
	return &StubProvider{
		inbox: make(map[Address][]InboundMessage),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// Ping is the boot readiness probe. The in-process stub is always ready.
func (p *StubProvider) Ping(context.Context) error { return nil }

// ResolveAddress is the European Digital Directory lookup. The stub trusts the
// identifier as-is.
func (p *StubProvider) ResolveAddress(_ context.Context, identifier string) (Address, error) {
	return Address(identifier), nil
}

// Send submits a message: it records submission + delivery evidence and enqueues
// the message for the recipient (local loopback).
func (p *StubProvider) Send(_ context.Context, msg OutboundMessage) (SendReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.seq++
	ref := p.ref(msg, p.seq)
	ts := p.now()

	submission := p.evidence(EvidenceSubmissionAcceptance, ref, ts)
	delivery := p.evidence(EvidenceDelivery, ref, ts)

	p.inbox[msg.Recipient] = append(p.inbox[msg.Recipient], InboundMessage{
		ProviderRef: ref,
		Sender:      msg.Sender,
		Recipient:   msg.Recipient,
		Subject:     msg.Subject,
		Body:        msg.Body,
		Attachments: msg.Attachments,
		Evidence:    []Evidence{delivery},
	})

	return SendReceipt{
		ProviderRef: ref,
		Status:      StatusDelivered,
		Evidence:    []Evidence{submission, delivery},
	}, nil
}

// Fetch drains and returns the inbound messages queued for an address.
func (p *StubProvider) Fetch(_ context.Context, addr Address) ([]InboundMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	msgs := p.inbox[addr]
	delete(p.inbox, addr)
	return msgs, nil
}

func (p *StubProvider) ref(msg OutboundMessage, seq int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", msg.Sender, msg.Recipient, msg.Subject, seq)))
	return "stub-" + hex.EncodeToString(sum[:8])
}

func (p *StubProvider) evidence(kind, ref string, ts time.Time) Evidence {
	raw := fmt.Sprintf(`{"type":%q,"providerRef":%q,"qualifiedTimestamp":%q}`, kind, ref, ts.Format(time.RFC3339Nano))
	return Evidence{
		Type:               kind,
		ProviderRef:        ref,
		QualifiedTimestamp: ts,
		Raw:                []byte(raw),
	}
}
