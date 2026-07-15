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
	ErrMessageNotFound = errors.New("qerds: message not found")
	ErrNoSenderAddress = errors.New("qerds: organization has no default digital address")
	ErrAddressNotFound = errors.New("qerds: digital address not found")
	ErrAddressTaken    = errors.New("qerds: digital address already taken")
)

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

// Address is an organization's QERDS unique digital address (Art 6(1)(j)).
type Address struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Address        string    `json:"address"`
	IsDefault      bool      `json:"isDefault"`
	ProviderRef    string    `json:"providerRef,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// MessageWithEvidence is a message plus its full evidence chain, for the detail
// view.
type MessageWithEvidence struct {
	Message
	Evidence []Evidence `json:"evidence"`
}
