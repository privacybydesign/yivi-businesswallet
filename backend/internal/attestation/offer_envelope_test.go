package attestation_test

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

func TestCredentialOfferEnvelopeRoundTrip(t *testing.T) {
	const (
		org     = "Acme B.V."
		cred    = "Supplier registration"
		offured = "openid-credential-offer://?credential_offer=%7B%22x%22%3A1%7D"
	)
	body, err := attestation.MarshalCredentialOfferEnvelope(org, cred, offured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	env, ok := attestation.ParseCredentialOfferEnvelope(body)
	if !ok {
		t.Fatalf("parse: expected the body to be recognised as an offer")
	}
	if env.CredentialOffer != offured {
		t.Errorf("CredentialOffer = %q, want %q", env.CredentialOffer, offured)
	}
	if env.SenderOrgName != org || env.CredentialName != cred {
		t.Errorf("metadata mismatch: %+v", env)
	}
}

func TestParseCredentialOfferEnvelopeRejectsNonOffer(t *testing.T) {
	cases := map[string]string{
		"plain human message": "Acme has offered your organization a credential. Open your wallet.",
		"empty":               "",
		"json but wrong type": `{"type":"something-else","credentialOffer":"openid-credential-offer://x"}`,
		"offer type no uri":   `{"type":"eaa-credential-offer/v1","credentialOffer":""}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := attestation.ParseCredentialOfferEnvelope(body); ok {
				t.Errorf("expected %q not to parse as a credential offer", body)
			}
		})
	}
}
