package attestation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// offerQueue is the pending-offer surface the receiver needs.
type offerQueue interface {
	RecordOffer(ctx context.Context, orgID uuid.UUID, in OfferInput) (CredentialOffer, bool, error)
}

// OfferReceiver consumes inbound QERDS messages: when the body carries an
// OpenID4VCI credential offer (a CredentialOfferEnvelope), it queues the offer
// for the receiving organization to accept or decline. Nothing is redeemed here —
// a credential enters the org's wallet only once an admin accepts (see
// Service.AcceptOffer). It is wired into qerds.Service as its InboundConsumer.
// Ordinary human messages pass through untouched.
//
// This is the receive half of the "OpenID4VCI offer over a secure channel"
// design (.ai/features/oid4vci-over-qerds.md): the send side ships the offer, the
// receiver's wallet redeems it — on the receiving org's say-so, not the sender's.
type OfferReceiver struct {
	offers offerQueue
	// trusted bounds whose offers are queued. See TrustedOfferSenders for why
	// the holder's issuer-trust validation does not make this redundant.
	trusted TrustedOfferSenders
}

func NewOfferReceiver(offers offerQueue, trusted TrustedOfferSenders) *OfferReceiver {
	return &OfferReceiver{offers: offers, trusted: trusted}
}

// OnInboundMessage implements qerds.InboundConsumer. It is idempotent: the queue
// keys on the source message, so a re-delivered offer neither queues twice nor
// reopens a decision the organization already made.
func (r *OfferReceiver) OnInboundMessage(ctx context.Context, in qerds.Inbound) error {
	orgID, messageID := in.OrgID, in.MessageID
	env, ok := ParseCredentialOfferEnvelope(in.Body)
	if !ok {
		return nil // not a credential offer — an ordinary QERDS message
	}

	// Both identities, not just the address: in.Sender is a property the sending
	// side wrote, in.FromParty is the AS4 party the gateway authenticated. See
	// TrustedOfferSenders.
	if !r.trusted.Trusts(in.FromParty, in.Sender) {
		// Not an error: the message is legitimately stored and stays in the org's
		// inbox for an operator to look at. Only the acceptance prompt is withheld,
		// so an untrusted sender cannot put an offer in front of an admin at all.
		slog.WarnContext(ctx, "attestation: credential offer from untrusted sender not queued",
			slog.String("orgId", orgID.String()),
			slog.String("fromParty", in.FromParty),
			slog.String("sender", in.Sender),
			slog.String("messageId", messageID.String()))
		return nil
	}

	offer, recorded, err := r.offers.RecordOffer(ctx, orgID, OfferInput{
		SourceMessageID: messageID,
		SenderOrgName:   env.SenderOrgName,
		SenderAddress:   in.Sender,
		FromParty:       in.FromParty,
		CredentialName:  env.CredentialName,
		Offer:           env.CredentialOffer,
	})
	if err != nil {
		return fmt.Errorf("attestation: queue offer from message %s org %s: %w", messageID, orgID, err)
	}
	if !recorded {
		return nil // queued on an earlier delivery of this message
	}

	slog.InfoContext(ctx, "attestation: queued QERDS credential offer for acceptance",
		slog.String("orgId", orgID.String()),
		slog.String("offerId", offer.ID.String()),
		slog.String("fromParty", in.FromParty),
		slog.String("sender", in.Sender),
		slog.String("messageId", messageID.String()))
	return nil
}
