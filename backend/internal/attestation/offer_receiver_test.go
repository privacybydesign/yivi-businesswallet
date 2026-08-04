package attestation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

type fakeRedeemer struct {
	calls    int
	gotOffer string
	result   eudiholder.Redeemed
	err      error
}

func (f *fakeRedeemer) Redeem(_ context.Context, _ uuid.UUID, offerURI string) (eudiholder.Redeemed, error) {
	f.calls++
	f.gotOffer = offerURI
	return f.result, f.err
}

type fakeHeldStore struct {
	exists   bool
	recorded []attestation.HeldInput
}

func (f *fakeHeldStore) HeldForMessage(_ context.Context, _, _ uuid.UUID) (bool, error) {
	// Mirror the real store: a message is "held" once a credential has been
	// recorded against it, so a re-delivery after a successful redeem is skipped.
	return f.exists || len(f.recorded) > 0, nil
}

func (f *fakeHeldStore) RecordHeld(_ context.Context, _ uuid.UUID, in attestation.HeldInput) (attestation.HeldAttestation, error) {
	f.recorded = append(f.recorded, in)
	return attestation.HeldAttestation{}, nil
}

func offerBody(t *testing.T) string {
	t.Helper()
	body, err := attestation.MarshalCredentialOfferEnvelope("Acme", "Registration", "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	return body
}

func TestOfferReceiverRedeemsAndRecords(t *testing.T) {
	redeemer := &fakeRedeemer{result: eudiholder.Redeemed{Ref: "ref-1", VCT: "nl.kvk.registration", Issuer: "https://issuer.test"}}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store)

	orgID, msgID := uuid.New(), uuid.New()
	if err := rec.OnInboundMessage(context.Background(), orgID, msgID, "subject", offerBody(t)); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}

	if redeemer.calls != 1 {
		t.Fatalf("expected 1 redeem call, got %d", redeemer.calls)
	}
	if redeemer.gotOffer != "openid-credential-offer://?x=1" {
		t.Errorf("redeemed offer = %q", redeemer.gotOffer)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("expected 1 held record, got %d", len(store.recorded))
	}
	got := store.recorded[0]
	if got.CredentialRef != "ref-1" || got.VCT != "nl.kvk.registration" || got.Source != attestation.HeldSourceQERDS {
		t.Errorf("held input mismatch: %+v", got)
	}
	if got.SourceMessageID == nil || *got.SourceMessageID != msgID {
		t.Errorf("SourceMessageID = %v, want %v", got.SourceMessageID, msgID)
	}
}

func TestOfferReceiverIgnoresNonOffer(t *testing.T) {
	redeemer := &fakeRedeemer{}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store)

	if err := rec.OnInboundMessage(context.Background(), uuid.New(), uuid.New(), "subject", "just a human message"); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if redeemer.calls != 0 || len(store.recorded) != 0 {
		t.Errorf("non-offer message must not redeem or record (redeems=%d, records=%d)", redeemer.calls, len(store.recorded))
	}
}

func TestOfferReceiverIdempotentWhenAlreadyHeld(t *testing.T) {
	redeemer := &fakeRedeemer{}
	store := &fakeHeldStore{exists: true}
	rec := attestation.NewOfferReceiver(redeemer, store)

	if err := rec.OnInboundMessage(context.Background(), uuid.New(), uuid.New(), "subject", offerBody(t)); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if redeemer.calls != 0 || len(store.recorded) != 0 {
		t.Errorf("already-held offer must be skipped (redeems=%d, records=%d)", redeemer.calls, len(store.recorded))
	}
}

// stagedRedeemer fails its first Redeem (mirroring an offer that arrives while
// the org's WSCA wallet is not yet activated) and succeeds on every call after,
// as it would once the wallet is activated.
type stagedRedeemer struct {
	calls  int
	result eudiholder.Redeemed
}

func (s *stagedRedeemer) Redeem(_ context.Context, _ uuid.UUID, offerURI string) (eudiholder.Redeemed, error) {
	s.calls++
	if s.calls == 1 {
		return eudiholder.Redeemed{}, errors.New("wsca: wallet not activated")
	}
	s.result.Ref = offerURI
	return s.result, nil
}

// An offer that arrives before the wallet is activated must not be lost: the
// first delivery fails and records nothing (so a re-delivery retries), a later
// delivery after activation redeems and records exactly one credential, and any
// further re-delivery is skipped by the idempotency guard.
func TestOfferReceiverRedeemableAfterActivation(t *testing.T) {
	redeemer := &stagedRedeemer{result: eudiholder.Redeemed{VCT: "nl.kvk.registration", Issuer: "https://issuer.test"}}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store)

	orgID, msgID := uuid.New(), uuid.New()
	body := offerBody(t)

	// Delivery 1: wallet not activated — redeem fails, nothing recorded.
	if err := rec.OnInboundMessage(context.Background(), orgID, msgID, "subject", body); err == nil {
		t.Fatal("expected an error while the wallet is not activated")
	}
	if len(store.recorded) != 0 {
		t.Fatalf("offer must not be recorded before activation, got %d", len(store.recorded))
	}

	// Delivery 2: wallet now activated — redeem succeeds and records the credential.
	if err := rec.OnInboundMessage(context.Background(), orgID, msgID, "subject", body); err != nil {
		t.Fatalf("OnInboundMessage after activation: %v", err)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("expected 1 held record after activation, got %d", len(store.recorded))
	}

	// Delivery 3: idempotency guard skips the already-held offer.
	if err := rec.OnInboundMessage(context.Background(), orgID, msgID, "subject", body); err != nil {
		t.Fatalf("OnInboundMessage on re-delivery: %v", err)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("re-delivery must not record again, got %d", len(store.recorded))
	}
	if redeemer.calls != 2 {
		t.Fatalf("expected 2 redeem attempts (fail, then succeed), got %d", redeemer.calls)
	}
}

func TestOfferReceiverReturnsRedeemError(t *testing.T) {
	redeemer := &fakeRedeemer{err: errors.New("token endpoint down")}
	store := &fakeHeldStore{}
	rec := attestation.NewOfferReceiver(redeemer, store)

	err := rec.OnInboundMessage(context.Background(), uuid.New(), uuid.New(), "subject", offerBody(t))
	if err == nil {
		t.Fatal("expected an error when redemption fails")
	}
	if len(store.recorded) != 0 {
		t.Errorf("must not record held on redeem failure, got %d", len(store.recorded))
	}
}
