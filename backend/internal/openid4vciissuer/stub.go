package openid4vciissuer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
)

const stubIssuanceIDBytes = 16

// StubIssuer is an in-process issuer for dev / CI (ATTESTATION_ISSUER=stub). It
// returns a deterministic-shaped offer and reports the credential as issued
// immediately, so the whole offer -> claim loop runs offline. It is the issuer
// equivalent of the QERDS StubProvider and the faked verifier. Selected by
// config, never code.
type StubIssuer struct{}

func NewStubIssuer() StubIssuer { return StubIssuer{} }

func (StubIssuer) CreateOffer(_ context.Context, req OfferRequest) (Offer, error) {
	id, err := randomID()
	if err != nil {
		return Offer{}, err
	}
	q := url.Values{}
	q.Set("credential", req.CredentialConfigID)
	q.Set("id", id)
	offer := Offer{
		IssuanceID: id,
		OfferURI:   "openid-credential-offer://stub?" + q.Encode(),
	}
	if req.UseTxCode {
		offer.TxCode = "000000"
	}
	return offer, nil
}

// Status reports the credential as issued immediately: the stub has no real
// wallet on the other end, so a poll resolves the offer for the demo/tests.
func (StubIssuer) Status(_ context.Context, _ string) (string, error) {
	return StatusIssued, nil
}

func (StubIssuer) Ping(context.Context) error { return nil }

func randomID() (string, error) {
	b := make([]byte, stubIssuanceIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("openid4vciissuer: stub id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
