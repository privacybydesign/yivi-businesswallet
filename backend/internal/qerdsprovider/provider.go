// Package qerdsprovider is the client seam to an external Qualified Electronic
// Registered Delivery Service (QERDS) provider. It mirrors internal/irmarequestor:
// our backend is a requestor / relying party talking to a qualified trust
// service over HTTP, never the trust service itself. The concrete provider is
// swapped by config — there is no in-process stub, so a real HTTP driver (the
// Domibus AS4 access point) is required in every environment — and the domain
// slice depends only on the value types and behaviours defined here. See
// .ai/features/qerds.md.
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
	Sender      Address
	Recipient   Address
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
