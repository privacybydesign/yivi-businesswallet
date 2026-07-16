// Package qerds is the domain slice for Qualified Electronic Registered Delivery
// Service communications (COM(2025) 838 Art 5(1)(i),(m),(n); eIDAS Art 43-44).
// It orchestrates persistence (messages, evidence, addresses), an external
// provider seam (internal/qerdsprovider) and auditing behind an org-scoped API.
// See .ai/features/qerds.md.
package qerds

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMessageNotFound    = errors.New("qerds: message not found")
	ErrAttachmentNotFound = errors.New("qerds: attachment not found")
	ErrNoSenderAddress    = errors.New("qerds: organization has no default digital address")
	ErrAddressNotFound    = errors.New("qerds: digital address not found")
	ErrAddressTaken       = errors.New("qerds: digital address already taken")
	ErrSenderNotOwned     = errors.New("qerds: sender address not owned by organization")

	ErrContactNotFound     = errors.New("qerds: contact not found")
	ErrContactAddressTaken = errors.New("qerds: contact address already saved")
)

// Contact is a saved recipient in an organization's address book — the interim
// stand-in for the European Digital Directory (name -> digital address).
type Contact struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Name           string    `json:"name"`
	Address        string    `json:"address"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Message is a QERDS communication owned by an organization.
type Message struct {
	ID                     uuid.UUID  `json:"id"`
	OrganizationID         uuid.UUID  `json:"organizationId"`
	Direction              string     `json:"direction"`
	SenderAddress          string     `json:"senderAddress"`
	RecipientAddress       string     `json:"recipientAddress"`
	Subject                string     `json:"subject"`
	Body                   string     `json:"body"`
	ProviderRef            string     `json:"providerRef,omitempty"`
	Status                 string     `json:"status"`
	SubmittedAt            *time.Time `json:"submittedAt,omitempty"`
	DeliveredAt            *time.Time `json:"deliveredAt,omitempty"`
	QualifiedTimestampSend *time.Time `json:"qualifiedTimestampSend,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

// Evidence is an append-only ERDS evidence record backing the Art 5(1)(n)
// "access, store and verify" dashboard.
type Evidence struct {
	ID                 uuid.UUID `json:"id"`
	MessageID          uuid.UUID `json:"messageId"`
	Type               string    `json:"type"`
	ProviderRef        string    `json:"providerRef"`
	QualifiedTimestamp time.Time `json:"qualifiedTimestamp"`
	Raw                []byte    `json:"raw"`
	CreatedAt          time.Time `json:"createdAt"`
}

// Attachment is a message payload's metadata. The bytes are content-opaque and
// fetched separately (download endpoint), never inlined in list/detail JSON:
// payloads are large and possibly E2E-encrypted. content_hash + size_bytes are
// the integrity metadata the ERDS store keeps.
type Attachment struct {
	ID          uuid.UUID `json:"id"`
	MessageID   uuid.UUID `json:"messageId"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	ContentHash string    `json:"contentHash"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Address is an organization's QERDS unique digital address (Art 6(1)(j)).
type Address struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Address        string    `json:"address"`
	IsDefault      bool      `json:"isDefault"`
	ProviderRef    string    `json:"providerRef,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// MessageWithEvidence is a message plus its attachment metadata and full
// evidence chain, for the detail view.
type MessageWithEvidence struct {
	Message
	Attachments []Attachment `json:"attachments"`
	Evidence    []Evidence   `json:"evidence"`
}
