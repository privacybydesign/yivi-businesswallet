package attestation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
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
}

func NewOfferReceiver(redeemer offerRedeemer, store heldRecorder) *OfferReceiver {
	return &OfferReceiver{redeemer: redeemer, store: store}
}

// OnInboundMessage implements qerds.InboundConsumer. It is idempotent: a message
// whose offer has already been redeemed (an active held row links it) is skipped,
// so a re-delivered offer is never redeemed twice.
func (r *OfferReceiver) OnInboundMessage(ctx context.Context, orgID, messageID uuid.UUID, _ string, body string) error {
	env, ok := ParseCredentialOfferEnvelope(body)
	if !ok {
		return nil // not a credential offer — an ordinary QERDS message
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
		slog.String("vct", redeemed.VCT),
		slog.String("messageId", messageID.String()))
	return nil
}
