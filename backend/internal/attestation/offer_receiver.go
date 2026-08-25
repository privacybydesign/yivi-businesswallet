package attestation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// offerRedeemer runs the holder OpenID4VCI flow to receive a credential from an
// offer (internal/eudiholder). Accept the interface so the receiver stays
// decoupled from the concrete engine (stub or irmago).
type offerRedeemer interface {
	Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (eudiholder.Redeemed, error)
}

// heldRecorder is the held-index surface the receiver needs.
type heldRecorder interface {
	HeldForMessage(ctx context.Context, orgID, messageID uuid.UUID) (bool, error)
	RecordHeld(ctx context.Context, orgID uuid.UUID, in HeldInput) (HeldAttestation, error)
}

// OfferReceiver consumes inbound QERDS messages: when the body carries an
// OpenID4VCI credential offer (a CredentialOfferEnvelope), it redeems the offer
// into the org's holder engine and indexes the received credential
// (source=qerds, linked to the message). It is wired into qerds.Service as its
// InboundConsumer. Ordinary human messages pass through untouched.
//
// This is the receive half of the "OpenID4VCI offer over a secure channel"
// design (.ai/features/oid4vci-over-qerds.md): the send side ships the offer, the
// receiver's wallet redeems it.
type OfferReceiver struct {
	redeemer offerRedeemer
	store    heldRecorder
	// trusted bounds whose offers are redeemed. See TrustedOfferSenders for why
	// the holder's issuer-trust validation does not make this redundant.
	trusted TrustedOfferSenders
}

func NewOfferReceiver(redeemer offerRedeemer, store heldRecorder, trusted TrustedOfferSenders) *OfferReceiver {
	return &OfferReceiver{redeemer: redeemer, store: store, trusted: trusted}
}

// OnInboundMessage implements qerds.InboundConsumer. It is idempotent: a message
// whose offer has already been redeemed (an active held row links it) is skipped,
// so a re-delivered offer is never redeemed twice.
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
		// inbox for an operator to look at. Only automatic redemption is withheld,
		// so an untrusted sender can never silently write into a wallet.
		slog.WarnContext(ctx, "attestation: credential offer from untrusted sender not redeemed",
			slog.String("orgId", orgID.String()),
			slog.String("fromParty", in.FromParty),
			slog.String("sender", in.Sender),
			slog.String("messageId", messageID.String()))
		return nil
	}

	already, err := r.store.HeldForMessage(ctx, orgID, messageID)
	if err != nil {
		return err
	}
	if already {
		return nil // redeemed on an earlier delivery of this message
	}

	redeemed, err := r.redeemer.Redeem(ctx, orgID, env.CredentialOffer)
	if err != nil {
		return fmt.Errorf("attestation: redeem offer from message %s org %s: %w", messageID, orgID, err)
	}

	msgID := messageID
	if _, err := r.store.RecordHeld(ctx, orgID, HeldInput{
		CredentialRef:   redeemed.Ref,
		VCT:             redeemed.VCT,
		Issuer:          redeemed.Issuer,
		Source:          HeldSourceQERDS,
		SourceMessageID: &msgID,
	}); err != nil {
		return err
	}

	slog.InfoContext(ctx, "attestation: redeemed QERDS credential offer",
		slog.String("orgId", orgID.String()),
		slog.String("fromParty", in.FromParty),
		slog.String("sender", in.Sender),
		slog.String("vct", redeemed.VCT),
		slog.String("messageId", messageID.String()))
	return nil
}
