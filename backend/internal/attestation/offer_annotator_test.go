package attestation_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

// fakeOfferLookupStore stands in for the offer queue's read side, keyed only on
// what OfferAnnotator asks for: the row backing one QERDS message.
type fakeOfferLookupStore struct {
	offer attestation.CredentialOffer
	err   error
}

func (f fakeOfferLookupStore) GetOfferBySourceMessage(_ context.Context, _, _ uuid.UUID) (attestation.CredentialOffer, error) {
	return f.offer, f.err
}

func TestOfferAnnotatorIgnoresNonOffer(t *testing.T) {
	a := attestation.NewOfferAnnotator(fakeOfferLookupStore{err: attestation.ErrOfferNotFound})

	_, body, ok := a.LookupOffer(context.Background(), uuid.New(), uuid.New(), "just a human message")
	if ok {
		t.Fatal("expected a non-offer body not to be recognised")
	}
	if body != "just a human message" {
		t.Errorf("body was rewritten for a non-offer message: %q", body)
	}
}

// The offer deeplink is a bearer token: it must never survive into the response
// the console reads, whether or not a credential_offers row exists for it yet.
func TestOfferAnnotatorRedactsDeeplinkAndAttachesQueuedStatus(t *testing.T) {
	body := offerBody(t)
	offerID := uuid.New()
	store := fakeOfferLookupStore{offer: attestation.CredentialOffer{ID: offerID, Status: attestation.OfferAccepted}}
	a := attestation.NewOfferAnnotator(store)

	ann, redacted, ok := a.LookupOffer(context.Background(), uuid.New(), uuid.New(), body)
	if !ok {
		t.Fatal("expected the offer envelope to be recognised")
	}
	if ann.SenderOrgName != "Acme" || ann.CredentialName != "Registration" {
		t.Errorf("envelope metadata not carried through: %+v", ann)
	}
	if ann.OfferID == nil || *ann.OfferID != offerID {
		t.Errorf("OfferID = %v, want %v", ann.OfferID, offerID)
	}
	if ann.Status != attestation.OfferAccepted {
		t.Errorf("Status = %q, want %q", ann.Status, attestation.OfferAccepted)
	}
	if strings.Contains(redacted, "openid-credential-offer://") {
		t.Errorf("redacted body still carries the deeplink: %q", redacted)
	}
	if !strings.Contains(redacted, "Acme") {
		t.Errorf("redacted body lost non-secret envelope metadata an operator debugging delivery still needs: %q", redacted)
	}
}

// Nothing queued yet — an untrusted sender, or the inbound consumer has not run
// for this delivery — must still redact: the raw body's deeplink is live either
// way, whether or not anything backs it in the offer queue.
func TestOfferAnnotatorRedactsEvenWhenNotQueued(t *testing.T) {
	body := offerBody(t)
	a := attestation.NewOfferAnnotator(fakeOfferLookupStore{err: attestation.ErrOfferNotFound})

	ann, redacted, ok := a.LookupOffer(context.Background(), uuid.New(), uuid.New(), body)
	if !ok {
		t.Fatal("expected the offer envelope to be recognised even without a queued row")
	}
	if ann.OfferID != nil {
		t.Errorf("OfferID = %v, want nil — nothing is queued to link to", ann.OfferID)
	}
	if strings.Contains(redacted, "openid-credential-offer://") {
		t.Errorf("redacted body still carries the deeplink: %q", redacted)
	}
}

func TestOfferAnnotatorRedactsOnLookupError(t *testing.T) {
	body := offerBody(t)
	a := attestation.NewOfferAnnotator(fakeOfferLookupStore{err: errors.New("database down")})

	ann, redacted, ok := a.LookupOffer(context.Background(), uuid.New(), uuid.New(), body)
	if !ok {
		t.Fatal("expected the offer envelope to still be recognised when the queue lookup errors")
	}
	if ann.OfferID != nil {
		t.Errorf("OfferID = %v, want nil on a lookup error", ann.OfferID)
	}
	if strings.Contains(redacted, "openid-credential-offer://") {
		t.Errorf("redacted body still carries the deeplink: %q", redacted)
	}
}
