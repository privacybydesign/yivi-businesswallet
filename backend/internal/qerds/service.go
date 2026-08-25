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
	AllAddresses(ctx context.Context) ([]Address, error)
}

// provider is the external QERDS provider seam (see internal/qerdsprovider).
type provider interface {
	Send(ctx context.Context, msg qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error)
	Fetch(ctx context.Context, addr qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error)
	ResolveAddress(ctx context.Context, identifier string) (qerdsprovider.Address, error)
}

// InboundConsumer is notified of each received message so a domain slice can act
// on its content — e.g. detect an OpenID4VCI credential offer in the body and
// redeem it into the org's holder engine (see internal/attestation). It is
// optional and best-effort: a consumer error is logged, never fatal, so it can
// never lose or reject an already-stored QERDS message. Implementations must be
// idempotent — the consumer runs again if the same message is re-delivered.
//
// sender is the message's originalSender address. It is passed because a
// consumer that acts on content — redeeming a credential offer, say — needs to
// know WHO delivered it: an offer is a bearer token, so once a foreign AS4 party
// can put messages on the network, "is this credential authentic" and "was this
// offer meant for this org" become different questions. Content validation
// answers only the first.
type InboundConsumer interface {
	OnInboundMessage(ctx context.Context, orgID, messageID uuid.UUID, sender, subject, body string) error
}

// Service coordinates the send flow, inbound intake and evidence persistence
// across the message store, address store and the external provider.
type Service struct {
	messages  messageStore
	addresses addressStore
	provider  provider
	consumer  InboundConsumer
}

func NewService(messages messageStore, addresses addressStore, prov provider) *Service {
	return &Service{messages: messages, addresses: addresses, provider: prov}
}

// SetInboundConsumer registers the (optional) consumer notified on every inbound
// message. Wire it at boot; nil leaves inbound intake as pure persistence.
func (s *Service) SetInboundConsumer(c InboundConsumer) { s.consumer = c }

// notifyConsumer runs the inbound consumer best-effort: a failure is logged and
// swallowed so it never rejects an already-persisted message. The consumer is
// idempotent, so a later re-delivery re-attempts a failed redemption.
func (s *Service) notifyConsumer(ctx context.Context, orgID uuid.UUID, msg Message) {
	if s.consumer == nil {
		return
	}
	if err := s.consumer.OnInboundMessage(ctx, orgID, msg.ID, msg.SenderAddress, msg.Subject, msg.Body); err != nil {
		slog.ErrorContext(ctx, "qerds inbound consumer failed",
			slog.String("messageId", msg.ID.String()),
			slog.String("sender", msg.SenderAddress),
			slog.String("error", err.Error()))
	}
}

// Send transmits a message via the provider. It persists the message
// (submitted) and audits before calling the provider, then applies the receipt.
// A provider failure leaves the message in a retryable "submitted" state rather
// than losing it — QERDS delivery is asynchronous.
// The from parameter is the chosen sending address; empty means "use the
// organization's default". A non-empty from must be one of the org's own
// addresses (ErrSenderNotOwned otherwise).
func (s *Service) Send(ctx context.Context, orgID uuid.UUID, from, recipient, subject, body string, attachments []qerdsprovider.Attachment) (Message, error) {
	sender, err := s.resolveSender(ctx, orgID, from)
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

// resolveSender picks the address a message is sent from: the org default when
// from is empty, otherwise the chosen address — which must be one the org owns.
func (s *Service) resolveSender(ctx context.Context, orgID uuid.UUID, from string) (Address, error) {
	if from == "" {
		return s.addresses.DefaultAddress(ctx, orgID)
	}
	owned, err := s.addresses.ListAddresses(ctx, orgID)
	if err != nil {
		return Address{}, err
	}
	for _, a := range owned {
		if a.Address == from {
			return a, nil
		}
	}
	return Address{}, ErrSenderNotOwned
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
			msg, created, err := s.messages.CreateInbound(ctx, orgID, in)
			if err != nil {
				return count, err
			}
			if created {
				count++
			}
			// Notify on every intake, not only newly-stored rows: the consumer is
			// idempotent and a re-delivered offer whose earlier redeem failed must
			// be retried. CreateInbound returns the existing row on a dedupe hit,
			// so msg is populated either way.
			s.notifyConsumer(ctx, orgID, msg)
		}
	}
	return count, nil
}

// PollAll drains inbound messages for every provisioned address on the
// deployment and returns how many were newly stored. It backs the background
// poller, so an offer pushed by a remote AS4 party is received without anyone
// being logged in — Poll only ever runs for an org whose console asks.
//
// A failure on one address is logged and the sweep continues: one org's
// unreachable address must not stop every other org from receiving. The last
// error is returned for the caller's log.
func (s *Service) PollAll(ctx context.Context) (int, error) {
	addresses, err := s.addresses.AllAddresses(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	var lastErr error
	for _, addr := range addresses {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		inbound, err := s.provider.Fetch(ctx, qerdsprovider.Address(addr.Address))
		if err != nil {
			lastErr = fmt.Errorf("qerds: fetch %q: %w", addr.Address, err)
			slog.ErrorContext(ctx, "qerds background poll: fetch failed",
				slog.String("address", addr.Address), slog.String("error", err.Error()))
			continue
		}
		for _, in := range inbound {
			// Attribute by the message's own finalRecipient when it names an
			// address we know, not by the address we happened to poll: the WS
			// plugin queue is shared across the access point, so a provider that
			// returns a message for another recipient must not have it filed
			// under this org.
			orgID := addr.OrganizationID
			if recipient := string(in.Recipient); recipient != "" && recipient != addr.Address {
				resolved, err := s.addresses.OrgByAddress(ctx, recipient)
				if err != nil {
					slog.WarnContext(ctx, "qerds background poll: unknown recipient, skipping",
						slog.String("polledAddress", addr.Address),
						slog.String("recipient", recipient))
					continue
				}
				orgID = resolved
			}

			msg, created, err := s.messages.CreateInbound(ctx, orgID, in)
			if err != nil {
				lastErr = err
				slog.ErrorContext(ctx, "qerds background poll: store failed",
					slog.String("address", addr.Address), slog.String("error", err.Error()))
				continue
			}
			if created {
				count++
			}
			s.notifyConsumer(ctx, orgID, msg)
		}
	}
	return count, lastErr
}

// ReceiveInbound stores a single message pushed by the provider (webhook path).
// It resolves the owning organization from the recipient address.
func (s *Service) ReceiveInbound(ctx context.Context, in qerdsprovider.InboundMessage) error {
	orgID, err := s.addresses.OrgByAddress(ctx, string(in.Recipient))
	if err != nil {
		return err
	}
	msg, _, err := s.messages.CreateInbound(ctx, orgID, in)
	if err != nil {
		return err
	}
	// Notify on every delivery (not only the first): the consumer is idempotent
	// and a re-delivered offer whose earlier redeem failed must be retried.
	s.notifyConsumer(ctx, orgID, msg)
	return nil
}
