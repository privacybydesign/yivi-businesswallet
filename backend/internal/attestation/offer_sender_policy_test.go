package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func TestTrustedOfferSendersMatching(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		sender   string
		want     bool
	}{
		// Unset must keep today's behaviour: a deployment that only exchanges
		// offers between its own orgs is unaffected by this policy existing.
		{"unset trusts anyone", nil, "whoever@wherever.test", true},
		{"unset trusts empty sender", nil, "", true},
		{"blank entries are not a config", []string{"", "  "}, "x@y.test", true},

		{"exact match", []string{"verid@partners.test"}, "verid@partners.test", true},
		{"exact mismatch", []string{"verid@partners.test"}, "evil@partners.test", false},
		{"case insensitive", []string{"verid@partners.test"}, "VerID@Partners.Test", true},
		{"domain wildcard", []string{"*@partners.test"}, "anyone@partners.test", true},
		{"domain wildcard other domain", []string{"*@partners.test"}, "anyone@evil.test", false},
		{"allow all", []string{"*"}, "anyone@evil.test", true},
		{"multiple patterns", []string{"a@x.test", "*@partners.test"}, "b@partners.test", true},

		// A configured allowlist must not be satisfiable by an unattributable
		// message: with no originalSender there is nothing to match.
		{"configured rejects empty sender", []string{"*@partners.test"}, "", false},
		// The domain pattern compares the domain part, not a bare suffix — else a
		// crafted local part defeats the allowlist.
		{"domain in local part", []string{"*@partners.test"}, "a@partners.test@evil.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := attestation.NewTrustedOfferSenders(tc.patterns)
			if got := p.Trusts(tc.sender); got != tc.want {
				t.Errorf("Trusts(%q) = %v, want %v", tc.sender, got, tc.want)
			}
		})
	}
}

func TestTrustedOfferSendersConfigured(t *testing.T) {
	if attestation.NewTrustedOfferSenders(nil).Configured() {
		t.Error("nil patterns must report not configured (boot warns on this)")
	}
	if attestation.NewTrustedOfferSenders([]string{" "}).Configured() {
		t.Error("blank-only patterns must report not configured")
	}
	if !attestation.NewTrustedOfferSenders([]string{"*"}).Configured() {
		t.Error(`["*"] is an explicit decision and must report configured`)
	}
}

// An offer delivered by a sender outside the allowlist must not be redeemed.
// This is the check that stops a foreign AS4 party replaying a genuine offer at
// an organization it was never meant for — content validation cannot, because
// the credential is authentic.
func TestOfferReceiverRejectsUntrustedSender(t *testing.T) {
	redeemer := &fakeRedeemer{result: eudiholder.Redeemed{Ref: "r", VCT: "v", Issuer: "i"}}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store,
		attestation.NewTrustedOfferSenders([]string{"verid@partners.test"}))

	// Not an error: the message stays in the inbox for an operator. Only the
	// automatic redemption is withheld.
	err := rec.OnInboundMessage(context.Background(), uuid.New(), uuid.New(),
		"attacker@evil.test", "subject", offerBody(t))
	if err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if redeemer.calls != 0 {
		t.Errorf("untrusted sender's offer was redeemed (%d calls)", redeemer.calls)
	}
	if len(store.recorded) != 0 {
		t.Errorf("untrusted sender's offer was recorded as held (%d records)", len(store.recorded))
	}
}

func TestOfferReceiverRedeemsTrustedSender(t *testing.T) {
	redeemer := &fakeRedeemer{result: eudiholder.Redeemed{Ref: "r", VCT: "v", Issuer: "i"}}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store,
		attestation.NewTrustedOfferSenders([]string{"*@partners.test"}))

	if err := rec.OnInboundMessage(context.Background(), uuid.New(), uuid.New(),
		"verid@partners.test", "subject", offerBody(t)); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if redeemer.calls != 1 {
		t.Fatalf("trusted sender's offer was not redeemed (%d calls)", redeemer.calls)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("trusted sender's offer was not recorded (%d records)", len(store.recorded))
	}
}
