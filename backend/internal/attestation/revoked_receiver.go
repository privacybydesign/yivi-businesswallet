package attestation

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

type heldRechecker interface {
	RecheckHeld(ctx context.Context, orgID uuid.UUID) (int, error)
}

// RevokedReceiver consumes the QERDS message an issuer sends when it revokes a
// credential the organization holds, and re-reads the organization's status
// lists at once instead of at the next scheduled re-check. Other messages pass
// through untouched.
type RevokedReceiver struct {
	held heldRechecker
}

func NewRevokedReceiver(held heldRechecker) *RevokedReceiver {
	return &RevokedReceiver{held: held}
}

// OnInboundMessage implements qerds.InboundConsumer. A failed re-check is
// logged, not returned: the scheduled re-check still catches the revocation.
func (r *RevokedReceiver) OnInboundMessage(ctx context.Context, in qerds.Inbound) error {
	if _, ok := ParseCredentialRevokedEnvelope(in.Body); !ok {
		return nil
	}
	changed, err := r.held.RecheckHeld(ctx, in.OrgID)
	if err != nil {
		slog.WarnContext(ctx, "attestation: re-check after revocation notice failed",
			slog.String("orgId", in.OrgID.String()), slog.String("sender", in.Sender), slog.Any("error", err))
		return nil
	}
	slog.InfoContext(ctx, "attestation: re-checked held credentials after revocation notice",
		slog.String("orgId", in.OrgID.String()), slog.String("sender", in.Sender), slog.Int("changed", changed))
	return nil
}
