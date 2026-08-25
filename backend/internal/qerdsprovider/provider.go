// Package qerdsprovider is the client seam to an external Qualified Electronic
// Registered Delivery Service (QERDS) provider. It mirrors internal/irmarequestor:
// our backend is a requestor / relying party talking to a qualified trust
// service over HTTP, never the trust service itself. The concrete provider is
// swapped by config — a StubProvider in dev/CI, a real HTTP driver in
// staging/prod — so the domain slice depends only on the value types and
// behaviours defined here. See .ai/features/qerds.md.
package qerdsprovider

import "time"

// Evidence-type constants model the ERDS evidence set (eIDAS Art 44 /
// ETSI EN 319 522). Each piece of evidence carries a qualified timestamp.
const (
	EvidenceSubmissionAcceptance = "submission-acceptance"
	EvidenceRelay                = "relay"
	EvidenceDelivery             = "delivery"
	EvidenceNonDelivery          = "non-delivery"
)

// Delivery-status constants a provider reports for a submitted message.
const (
	StatusSubmitted = "submitted"
	StatusAccepted  = "accepted"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
)

// Address is a QERDS unique digital address (eIDAS Art 6(1)(j)).
type Address string

// Attachment is a message payload handed to (or received from) the provider.
// Content is opaque bytes — possibly E2E-encrypted ciphertext — carried
// verbatim; the provider treats it as an ERDS payload part.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// OutboundMessage is a message handed to the provider for delivery.
type OutboundMessage struct {
	Sender      Address
	Recipient   Address
	Subject     string
	Body        string
	Attachments []Attachment
}

// SendReceipt is what the provider returns for an accepted submission.
type SendReceipt struct {
	ProviderRef string
	Status      string
	Evidence    []Evidence
}

// InboundMessage is a message pulled from (or pushed by) the provider.
type InboundMessage struct {
	ProviderRef string
	// FromParty is the transport-level sender: the ebMS3 From PartyId of the
	// access point that delivered the message. Unlike Sender it is not something
	// the sender can choose freely — the receiving gateway only accepts a
	// UserMessage whose From party is in its PMode and whose signature chains to
	// that party's certificate, so this is the one sender identity that has been
	// cryptographically verified by the time we see the message.
	//
	// Empty for providers that do not expose a transport party.
	FromParty string
	// Sender is the business-level sender: the originalSender message property,
	// i.e. the QERDS digital address. The SENDING side populates it, so a party
	// admitted by the PMode can put any value here. Never make a trust decision
	// on it alone — see attestation.TrustedOfferSenders.
	Sender    Address
	Recipient Address
	Subject     string
	Body        string
	Attachments []Attachment
	Evidence    []Evidence
}

// Evidence is a single tamper-evident ERDS evidence record.
type Evidence struct {
	Type               string
	ProviderRef        string
	QualifiedTimestamp time.Time
	Raw                []byte
}
