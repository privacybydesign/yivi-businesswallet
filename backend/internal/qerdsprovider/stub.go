package qerdsprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// refBytes is the length of the random provider-ref suffix.
const refBytes = 8

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

	ref := p.ref()
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

// ref mints a unique provider reference per submission, like a real QERDS
// provider (each accepted message gets its own ERDS message id). It must be
// globally unique — deriving it from message fields plus an in-memory counter
// collided across process restarts (the counter reset), so a repeated send hit
// the UNIQUE(direction, provider_ref) index and stuck the message in "submitted".
func (p *StubProvider) ref() string {
	var b [refBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; fall back to a timestamp so we still
		// return a usable, near-unique ref rather than panicking.
		return "stub-" + hex.EncodeToString([]byte(p.now().Format(time.RFC3339Nano)))
	}
	return "stub-" + hex.EncodeToString(b[:])
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
