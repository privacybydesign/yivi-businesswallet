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
// It therefore gates on TWO identities, because they are trustworthy to very
// different degrees:
//
//   - The AS4 party (ebMS3 From PartyId) that delivered the message. The
//     receiving gateway only accepts a UserMessage whose party is in its PMode
//     and whose signature chains to that party's certificate, so this identity
//     is cryptographically verified before we ever see the message. It is the
//     only one that can bound WHO may hand us offers.
//   - The originalSender digital address. The sending side writes this into a
//     message property, so any party the PMode admits can claim any address.
//     Gating on it alone is not a boundary: a peered party that is allowed to
//     send us anything can simply claim an allowlisted address. It refines the
//     decision within a party; it cannot make it.
//
// Both lists are optional and applied with AND, so a deployment can configure
// the party bound and leave addresses open, or vice versa. Configuring neither
// trusts every sender.
//
// Address matching mirrors the QERDS address shape: an exact address, "*@domain"
// for a whole address domain, or "*" for any sender. Party ids are opaque, so
// they match exactly (case-folded), with "*" for any party.
type TrustedOfferSenders struct {
	// patterns is nil when no address allowlist is configured, which means "any
	// originalSender" — today's behaviour, preserved so enabling this file changes
	// nothing for a deployment that only exchanges offers between its own orgs.
	patterns []string
	// parties is nil when no party allowlist is configured, which means "any
	// party the PMode admits". Any deployment peering with an external AS4 party
	// must configure it: it is the half of this policy that a remote sender
	// cannot talk its way past.
	parties []string
}

// NewTrustedOfferSenders builds the policy from configured sender-address
// patterns and AS4 party ids. Empty lists (or ones containing only blank
// entries) yield an "allow all" policy for that half; callers should surface
// that at boot, since it is safe only while every sender is an organization on
// this deployment.
func NewTrustedOfferSenders(patterns, parties []string) TrustedOfferSenders {
	return TrustedOfferSenders{patterns: cleanPolicyList(patterns), parties: cleanPolicyList(parties)}
}

func cleanPolicyList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			cleaned = append(cleaned, v)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// Configured reports whether a sender-address allowlist is in force. False means
// every originalSender is accepted.
func (t TrustedOfferSenders) Configured() bool { return len(t.patterns) > 0 }

// Patterns returns the configured sender-address patterns, for the boot log.
func (t TrustedOfferSenders) Patterns() []string { return t.patterns }

// PartiesConfigured reports whether an AS4 party allowlist is in force. False
// means any party the PMode admits may deliver a redeemable offer.
func (t TrustedOfferSenders) PartiesConfigured() bool { return len(t.parties) > 0 }

// Parties returns the configured AS4 party ids, for the boot log.
func (t TrustedOfferSenders) Parties() []string { return t.parties }

// Trusts reports whether an offer may be redeemed. fromParty is the verified
// ebMS3 From PartyId (empty when the provider exposes none); sender is the
// originalSender address.
func (t TrustedOfferSenders) Trusts(fromParty, sender string) bool {
	if !t.trustsParty(fromParty) {
		return false
	}
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

// trustsParty checks the delivering AS4 party against the party allowlist.
func (t TrustedOfferSenders) trustsParty(fromParty string) bool {
	if !t.PartiesConfigured() {
		return true
	}
	fromParty = strings.ToLower(strings.TrimSpace(fromParty))
	if fromParty == "" {
		// No transport identity at all: nothing to check the allowlist against, so
		// an allowlist that is deliberately in force must reject it rather than
		// fall back to the property the sender wrote.
		return false
	}
	for _, p := range t.parties {
		if p == "*" || p == fromParty {
			return true
		}
	}
	return false
}
