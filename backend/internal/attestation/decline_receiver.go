package attestation

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

type declineMarker interface {
	MarkDeclined(ctx context.Context, orgID uuid.UUID, offerHash, sender string) (Issued, error)
}

// DeclineReceiver consumes the QERDS message a receiving organization sends
// back when it declines an offer, and marks the issued offer declined. Other
// messages pass through untouched.
type DeclineReceiver struct {
	issued declineMarker
}

func NewDeclineReceiver(issued declineMarker) *DeclineReceiver {
	return &DeclineReceiver{issued: issued}
}

// OnInboundMessage implements qerds.InboundConsumer. A re-delivered decline
// finds the offer already declined and is ignored.
func (r *DeclineReceiver) OnInboundMessage(ctx context.Context, in qerds.Inbound) error {
	env, ok := ParseOfferDeclinedEnvelope(in.Body)
	if !ok {
		return nil
	}
	issued, err := r.issued.MarkDeclined(ctx, in.OrgID, env.OfferHash, in.Sender)
	if errors.Is(err, ErrIssuedNotFound) {
		slog.WarnContext(ctx, "attestation: decline matches no open offer",
			slog.String("orgId", in.OrgID.String()), slog.String("sender", in.Sender),
			slog.String("messageId", in.MessageID.String()))
		return nil
	}
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "attestation: offer declined by recipient",
		slog.String("orgId", in.OrgID.String()), slog.String("issuedId", issued.ID.String()),
		slog.String("sender", in.Sender))
	return nil
}
