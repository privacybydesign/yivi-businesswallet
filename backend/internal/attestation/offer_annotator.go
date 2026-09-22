package attestation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// redactedOfferPlaceholder replaces CredentialOffer in a body OfferAnnotator
// hands back. The deeplink is a one-time bearer token (see the CredentialOffer
// type comment), so it never reaches the console — not in the API response and
// not behind the QERDS message screen's "view raw" toggle.
const redactedOfferPlaceholder = "[redacted — replayed server-side on accept]"

// offerLookupStore is the read OfferAnnotator needs from the offer queue: the
// same row a decision endpoint would see, keyed by the QERDS message that
// carried the offer in.
type offerLookupStore interface {
	GetOfferBySourceMessage(ctx context.Context, orgID, messageID uuid.UUID) (CredentialOffer, error)
}

// OfferAnnotator adapts the attestation offer queue to qerds.Handler's
// offerLookup seam (qerds.Handler.SetOfferLookup): given a QERDS message body,
// it recognises a credential-offer envelope, redacts the one-time deeplink out
// of it, and — if a credential_offers row exists for the message — attaches the
// offer's id and decision status so the console can link through to it.
//
// This is the read-side counterpart to OfferReceiver, which is the write side.
type OfferAnnotator struct {
	offers offerLookupStore
}

func NewOfferAnnotator(offers offerLookupStore) *OfferAnnotator {
	return &OfferAnnotator{offers: offers}
}

// LookupOffer implements qerds.Handler's offerLookup.
func (a *OfferAnnotator) LookupOffer(ctx context.Context, orgID, messageID uuid.UUID, body string) (qerds.CredentialOfferAnnotation, string, bool) {
	env, ok := ParseCredentialOfferEnvelope(body)
	if !ok {
		return qerds.CredentialOfferAnnotation{}, body, false
	}

	ann := qerds.CredentialOfferAnnotation{
		SenderOrgName:  env.SenderOrgName,
		CredentialName: env.CredentialName,
		Message:        env.Message,
	}
	offer, err := a.offers.GetOfferBySourceMessage(ctx, orgID, messageID)
	switch {
	case err == nil:
		id := offer.ID
		ann.OfferID = &id
		ann.Status = offer.Status
	case errors.Is(err, ErrOfferNotFound):
		// Not queued — an untrusted sender (see TrustedOfferSenders), or the
		// inbound consumer has not run for this delivery yet. Still redact: the
		// deeplink in the body is live either way. Nothing to link to, so OfferID
		// and Status stay unset.
	default:
		slog.ErrorContext(ctx, "attestation: look up offer for qerds message annotation",
			slog.String("error", err.Error()),
			slog.String("orgId", orgID.String()),
			slog.String("messageId", messageID.String()))
	}

	env.CredentialOffer = redactedOfferPlaceholder
	redacted, err := json.Marshal(env)
	if err != nil {
		slog.ErrorContext(ctx, "attestation: redact qerds credential offer body",
			slog.String("error", err.Error()), slog.String("messageId", messageID.String()))
		return ann, redactedOfferPlaceholder, true
	}
	return ann, string(redacted), true
}
