package openid4vppresenter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// Receiver consumes inbound QERDS messages: when the body carries an OpenID4VP
// Authorization Request (a PresentationRequestEnvelope), it validates the
// invocation the same way Service.Start does for a browser and queues it,
// already bound to the receiving organization, for an admin to approve or
// decline (Service.Approve / Service.Decline). It is wired into qerds.Service as
// (part of) its InboundConsumer. An ordinary human message, or one whose request
// object fails validation, is left alone or logged — never rejected, since the
// QERDS message itself is already stored either way.
//
// This is the receive half of "credential disclosure between orgs over QERDS"
// (issue #271): the requesting org sends the invocation, the receiving org's
// wallet presents on its own say-so, never the sender's.
type Receiver struct {
	svc *Service
}

func NewReceiver(svc *Service) *Receiver {
	return &Receiver{svc: svc}
}

// OnInboundMessage implements qerds.InboundConsumer. It is idempotent: the
// service keys the queued request on the source message, so a re-delivered
// request neither queues twice nor reopens a decision the organization already
// made.
func (r *Receiver) OnInboundMessage(ctx context.Context, in qerds.Inbound) error {
	env, ok := ParsePresentationRequestEnvelope(in.Body)
	if !ok {
		return nil // not a presentation request — an ordinary QERDS message
	}

	t, recorded, err := r.svc.ReceiveFromQERDS(ctx, in.OrgID, in.MessageID, StartRequest{
		ClientID:         env.ClientID,
		RequestURI:       env.RequestURI,
		RequestURIMethod: env.RequestURIMethod,
	})
	if err != nil {
		if isInvocationError(err) {
			// The sender's invocation itself is malformed, unreachable or fails
			// validation — nothing an admin could act on. The QERDS message is
			// already stored, so this is an unactionable delivery, not a lost one.
			slog.WarnContext(ctx, "openid4vppresenter: presentation request over QERDS not queued",
				slog.String("orgId", in.OrgID.String()),
				slog.String("messageId", in.MessageID.String()),
				slog.String("error", err.Error()))
			return nil
		}
		return fmt.Errorf("openid4vppresenter: queue presentation request from message %s org %s: %w", in.MessageID, in.OrgID, err)
	}
	if !recorded {
		return nil // queued on an earlier delivery of this message
	}

	slog.InfoContext(ctx, "openid4vppresenter: queued QERDS presentation request for approval",
		slog.String("orgId", in.OrgID.String()),
		slog.String("transactionId", t.ID.String()),
		slog.String("messageId", in.MessageID.String()))
	return nil
}

// isInvocationError reports whether err means the sender's Authorization Request
// invocation is unusable, as opposed to a store/infrastructure failure that
// should be retried by re-processing a re-delivered message.
func isInvocationError(err error) bool {
	return errors.Is(err, ErrInvalidRequest) ||
		errors.Is(err, ErrInvalidRequestURIMethod) ||
		errors.Is(err, ErrInvalidRequestObject) ||
		errors.Is(err, ErrRequestURIUnreachable)
}
