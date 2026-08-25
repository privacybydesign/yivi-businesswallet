package attestation

import (
	"strings"
)

// TrustedOfferSenders decides whose inbound QERDS credential offers may be
// redeemed automatically.
//
// This is deliberately separate from issuer trust. The holder already validates
// the credential it fetches: issuer x5c chain, signature, validity, status list,
// and holder binding (see eudiholder). That
// answers "is this credential authentic". It cannot answer "was this offer meant
// for this organization", because a credential offer is a BEARER TOKEN — whoever
// redeems the pre-authorized code gets the credential.
//
// Concretely: a legitimate offer minted for org A, replayed by any party on the
// AS4 network with finalRecipient=orgB, yields a genuinely-signed,
// correctly-chained, unrevoked credential bound to org B's key. Every content
// check passes; it simply landed in the wrong wallet. That is what this policy
// exists to stop, and only the transport identity can.
//
// Matching mirrors the QERDS address shape: an exact address, "*@domain" for a
// whole address domain, or "*" for any sender.
type TrustedOfferSenders struct {
	// patterns is nil when no allowlist is configured, which means "trust every
	// sender" — today's behaviour, preserved so enabling this file changes
	// nothing for a deployment that only exchanges offers between its own orgs.
	// Any deployment peering with an external AS4 party must configure it.
	patterns []string
}

// NewTrustedOfferSenders builds the policy from configured patterns. An empty
// list (or one containing only blank entries) yields an "allow all" policy;
// callers should surface that at boot, since it is safe only while every sender
// is an organization on this deployment.
func NewTrustedOfferSenders(patterns []string) TrustedOfferSenders {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return TrustedOfferSenders{}
	}
	return TrustedOfferSenders{patterns: cleaned}
}

// Configured reports whether an allowlist is in force. False means every sender
// is trusted.
func (t TrustedOfferSenders) Configured() bool { return len(t.patterns) > 0 }

// Patterns returns the configured patterns, for the boot log.
func (t TrustedOfferSenders) Patterns() []string { return t.patterns }

// Trusts reports whether an offer delivered by sender may be redeemed.
func (t TrustedOfferSenders) Trusts(sender string) bool {
	if !t.Configured() {
		return true
	}
	sender = strings.ToLower(strings.TrimSpace(sender))
	if sender == "" {
		// An offer with no originalSender cannot be attributed, so it cannot be
		// matched against an allowlist that is deliberately in force.
		return false
	}
	for _, p := range t.patterns {
		if p == "*" || p == sender {
			return true
		}
		// "*@domain" compares the domain part specifically. Matching a bare
		// suffix would let a crafted local part such as
		// "a@trusted.example@evil.test" slip through.
		if domain, ok := strings.CutPrefix(p, "*@"); ok {
			if _, got, found := strings.Cut(sender, "@"); found && got == domain {
				return true
			}
		}
	}
	return false
}
