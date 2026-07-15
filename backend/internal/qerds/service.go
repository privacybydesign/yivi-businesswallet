package qerds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// messageStore is the write/coordination surface the service needs; reads for
// the API go through the store directly from the handler.
type messageStore interface {
	CreateOutbound(ctx context.Context, orgID uuid.UUID, sender, recipient, subject, body string, attachments []qerdsprovider.Attachment) (Message, error)
	RecordSent(ctx context.Context, messageID uuid.UUID, receipt qerdsprovider.SendReceipt) error
	CreateInbound(ctx context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (Message, bool, error)
}

type addressStore interface {
	DefaultAddress(ctx context.Context, orgID uuid.UUID) (Address, error)
	ListAddresses(ctx context.Context, orgID uuid.UUID) ([]Address, error)
	OrgByAddress(ctx context.Context, address string) (uuid.UUID, error)
}

// provider is the external QERDS provider seam (see internal/qerdsprovider).
type provider interface {
	Send(ctx context.Context, msg qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error)
	Fetch(ctx context.Context, addr qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error)
	ResolveAddress(ctx context.Context, identifier string) (qerdsprovider.Address, error)
}

// Service coordinates the send flow, inbound intake and evidence persistence
// across the message store, address store and the external provider.
type Service struct {
	messages  messageStore
	addresses addressStore
	provider  provider
}

func NewService(messages messageStore, addresses addressStore, prov provider) *Service {
	return &Service{messages: messages, addresses: addresses, provider: prov}
}

// Send transmits a message via the provider. It persists the message
// (submitted) and audits before calling the provider, then applies the receipt.
// A provider failure leaves the message in a retryable "submitted" state rather
// than losing it — QERDS delivery is asynchronous.
func (s *Service) Send(ctx context.Context, orgID uuid.UUID, recipient, subject, body string, attachments []qerdsprovider.Attachment) (Message, error) {
	sender, err := s.addresses.DefaultAddress(ctx, orgID)
	if err != nil {
		return Message{}, err
	}

	resolved, err := s.provider.ResolveAddress(ctx, recipient)
	if err != nil {
		return Message{}, fmt.Errorf("qerds: resolve recipient %q: %w", recipient, err)
	}

	msg, err := s.messages.CreateOutbound(ctx, orgID, sender.Address, string(resolved), subject, body, attachments)
	if err != nil {
		return Message{}, err
	}

	receipt, err := s.provider.Send(ctx, qerdsprovider.OutboundMessage{
		Sender:      qerdsprovider.Address(sender.Address),
		Recipient:   resolved,
		Subject:     subject,
		Body:        body,
		Attachments: attachments,
	})
	if err != nil {
		// Persisted and audited; retryable. Surface as the submitted message.
		slog.ErrorContext(ctx, "qerds provider send failed; message left submitted",
			slog.String("messageId", msg.ID.String()), slog.String("error", err.Error()))
		return msg, nil
	}

	if err := s.messages.RecordSent(ctx, msg.ID, receipt); err != nil {
		return Message{}, err
	}

	// Reflect the receipt in the returned message without a re-read.
	msg.ProviderRef = receipt.ProviderRef
	msg.Status = StatusAccepted
	for _, e := range receipt.Evidence {
		if e.Type == qerdsprovider.EvidenceDelivery {
			ts := e.QualifiedTimestamp
			msg.Status = StatusDelivered
			msg.DeliveredAt = &ts
		}
	}
	return msg, nil
}

// Poll pulls new inbound messages for all of an organization's addresses and
// returns how many were newly stored. Intake is idempotent (dedupe on provider
// ref), so repeated polls are safe.
func (s *Service) Poll(ctx context.Context, orgID uuid.UUID) (int, error) {
	addresses, err := s.addresses.ListAddresses(ctx, orgID)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, addr := range addresses {
		inbound, err := s.provider.Fetch(ctx, qerdsprovider.Address(addr.Address))
		if err != nil {
			return count, fmt.Errorf("qerds: fetch %q: %w", addr.Address, err)
		}
		for _, in := range inbound {
			_, created, err := s.messages.CreateInbound(ctx, orgID, in)
			if err != nil {
				return count, err
			}
			if created {
				count++
			}
		}
	}
	return count, nil
}

// ReceiveInbound stores a single message pushed by the provider (webhook path).
// It resolves the owning organization from the recipient address.
func (s *Service) ReceiveInbound(ctx context.Context, in qerdsprovider.InboundMessage) error {
	orgID, err := s.addresses.OrgByAddress(ctx, string(in.Recipient))
	if err != nil {
		return err
	}
	if _, _, err := s.messages.CreateInbound(ctx, orgID, in); err != nil {
		return err
	}
	return nil
}
