package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

func TestTrustedOfferSendersMatching(t *testing.T) {
	for _, tc := range []struct {
		name      string
		patterns  []string
		parties   []string
		fromParty string
		sender    string
		want      bool
	}{
		// Unset must keep today's behaviour: a deployment that only exchanges
		// offers between its own orgs is unaffected by this policy existing.
		{"unset trusts anyone", nil, nil, "verid-qerds", "whoever@wherever.test", true},
		{"unset trusts empty sender", nil, nil, "verid-qerds", "", true},
		{"blank entries are not a config", []string{"", "  "}, nil, "verid-qerds", "x@y.test", true},

		{"exact match", []string{"verid@partners.test"}, nil, "verid-qerds", "verid@partners.test", true},
		{"exact mismatch", []string{"verid@partners.test"}, nil, "verid-qerds", "evil@partners.test", false},
		{"case insensitive", []string{"verid@partners.test"}, nil, "verid-qerds", "VerID@Partners.Test", true},
		{"domain wildcard", []string{"*@partners.test"}, nil, "verid-qerds", "anyone@partners.test", true},
		{"domain wildcard other domain", []string{"*@partners.test"}, nil, "verid-qerds", "anyone@evil.test", false},
		{"allow all", []string{"*"}, nil, "verid-qerds", "anyone@evil.test", true},
		{"multiple patterns", []string{"a@x.test", "*@partners.test"}, nil, "verid-qerds", "b@partners.test", true},

		// A configured allowlist must not be satisfiable by an unattributable
		// message: with no originalSender there is nothing to match.
		{"configured rejects empty sender", []string{"*@partners.test"}, nil, "verid-qerds", "", false},
		// The domain pattern compares the domain part, not a bare suffix — else a
		// crafted local part defeats the allowlist.
		{"domain in local part", []string{"*@partners.test"}, nil, "verid-qerds", "a@partners.test@evil.test", false},

		// The party half. This is the one a remote sender cannot claim its way
		// past: originalSender is a property the sender writes, so an allowlisted
		// address proves nothing on its own.
		{"party allowlist admits its party", nil, []string{"verid-qerds"}, "verid-qerds", "verid@partners.test", true},
		{"party allowlist rejects another party", nil, []string{"verid-qerds"}, "evil-party", "verid@partners.test", false},
		{"party match is case insensitive", nil, []string{"verid-qerds"}, "VERID-QERDS", "x@y.test", true},
		{"party wildcard", nil, []string{"*"}, "whoever_gw", "x@y.test", true},
		// No transport identity at all: a configured party allowlist must not fall
		// back to the sender-written property.
		{"configured party rejects empty party", nil, []string{"verid-qerds"}, "", "verid@partners.test", false},
		{"unset party tolerates empty party", nil, nil, "", "verid@partners.test", true},

		// ANDed: a spoofed originalSender does not survive the party check, and an
		// admitted party does not bypass the address check.
		{"spoofed sender from wrong party", []string{"verid@partners.test"}, []string{"verid-qerds"}, "evil-party", "verid@partners.test", false},
		{"right party, unlisted sender", []string{"verid@partners.test"}, []string{"verid-qerds"}, "verid-qerds", "someone@partners.test", false},
		{"right party, listed sender", []string{"verid@partners.test"}, []string{"verid-qerds"}, "verid-qerds", "verid@partners.test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := attestation.NewTrustedOfferSenders(tc.patterns, tc.parties)
			if got := p.Trusts(tc.fromParty, tc.sender); got != tc.want {
				t.Errorf("Trusts(%q, %q) = %v, want %v", tc.fromParty, tc.sender, got, tc.want)
			}
		})
	}
}

func TestTrustedOfferSendersConfigured(t *testing.T) {
	if attestation.NewTrustedOfferSenders(nil, nil).Configured() {
		t.Error("nil patterns must report not configured (boot warns on this)")
	}
	if attestation.NewTrustedOfferSenders([]string{" "}, nil).Configured() {
		t.Error("blank-only patterns must report not configured")
	}
	if !attestation.NewTrustedOfferSenders([]string{"*"}, nil).Configured() {
		t.Error(`["*"] is an explicit decision and must report configured`)
	}

	// The party half reports separately: boot warns about each, and a deployment
	// may reasonably configure one and not the other.
	if attestation.NewTrustedOfferSenders([]string{"*"}, nil).PartiesConfigured() {
		t.Error("nil parties must report not configured (boot warns on this)")
	}
	if attestation.NewTrustedOfferSenders(nil, []string{"  "}).PartiesConfigured() {
		t.Error("blank-only parties must report not configured")
	}
	p := attestation.NewTrustedOfferSenders(nil, []string{"Verid-QERDS"})
	if !p.PartiesConfigured() {
		t.Error("a configured party list must report configured")
	}
	if got := p.Parties(); len(got) != 1 || got[0] != "verid-qerds" {
		t.Errorf("Parties() = %v, want the normalized [verid-qerds] for the boot log", got)
	}
}

// An offer delivered by a sender outside the allowlist must not be redeemed.
// This is the check that stops a foreign AS4 party replaying a genuine offer at
// an organization it was never meant for — content validation cannot, because
// the credential is authentic.
func TestOfferReceiverRejectsUntrustedSender(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue,
		attestation.NewTrustedOfferSenders([]string{"verid@partners.test"}, nil))

	// Not an error: the message stays in the inbox for an operator. Only the
	// acceptance prompt is withheld.
	err := rec.OnInboundMessage(context.Background(),
		inbound(uuid.New(), uuid.New(), "verid-qerds", "attacker@evil.test", offerBody(t)))
	if err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(queue.queued()) != 0 {
		t.Errorf("untrusted sender's offer was queued for acceptance (%d queued)", len(queue.queued()))
	}
}

// The sender allowlist is matched against originalSender, which the SENDING side
// writes. So an address allowlist alone is not a boundary: any party the PMode
// admits can claim an allowlisted address. The party allowlist is what actually
// bounds it, and it must reject the delivery even though every address on the
// message is one we trust.
func TestOfferReceiverRejectsSpoofedSenderFromUntrustedParty(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue,
		attestation.NewTrustedOfferSenders([]string{"verid@partners.test"}, []string{"verid-qerds"}))

	err := rec.OnInboundMessage(context.Background(),
		inbound(uuid.New(), uuid.New(), "evil-party", "verid@partners.test", offerBody(t)))
	if err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(queue.queued()) != 0 {
		t.Errorf("an unlisted AS4 party claiming a trusted originalSender got its offer queued (%d queued)", len(queue.queued()))
	}
}

// A provider that exposes no transport party cannot satisfy a party allowlist
// that is deliberately in force — falling back to the sender-written property
// would defeat the point of configuring it.
func TestOfferReceiverRejectsMissingPartyWhenPartiesConfigured(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue,
		attestation.NewTrustedOfferSenders(nil, []string{"verid-qerds"}))

	if err := rec.OnInboundMessage(context.Background(),
		inbound(uuid.New(), uuid.New(), "", "verid@partners.test", offerBody(t))); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(queue.queued()) != 0 {
		t.Errorf("offer with no transport party was queued (%d queued)", len(queue.queued()))
	}
}

func TestOfferReceiverQueuesTrustedSender(t *testing.T) {
	queue := newFakeOfferQueue()
	rec := attestation.NewOfferReceiver(queue,
		attestation.NewTrustedOfferSenders([]string{"*@partners.test"}, []string{"verid-qerds"}))

	if err := rec.OnInboundMessage(context.Background(),
		inbound(uuid.New(), uuid.New(), "verid-qerds", "verid@partners.test", offerBody(t))); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(queue.queued()) != 1 {
		t.Fatalf("trusted sender's offer was not queued (%d queued)", len(queue.queued()))
	}
}
