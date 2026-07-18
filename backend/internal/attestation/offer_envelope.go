package attestation

import (
	"encoding/json"
	"fmt"
)

// credentialOfferEnvelopeType discriminates a machine-consumable EAA credential
// offer carried in a QERDS message body from an ordinary human message. Bumped
// if the envelope shape changes so an older receiver ignores an incompatible one.
const credentialOfferEnvelopeType = "eaa-credential-offer/v1"

// CredentialOfferEnvelope is the structured QERDS body that carries an OpenID4VCI
// credential offer between business wallets, replacing the earlier plaintext
// claim-link notification. The offer travels as the message body (a structured
// part), not an attachment; the receiver detects it by Type and redeems
// CredentialOffer through the holder OpenID4VCI flow (pre-authorized-code grant).
type CredentialOfferEnvelope struct {
	Type           string `json:"type"`
	SenderOrgName  string `json:"senderOrgName"`
	CredentialName string `json:"credentialName"`
	// CredentialOffer is the OpenID4VCI credential-offer deeplink
	// (openid-credential-offer://…). Self-contained: it encodes the credential
	// issuer URL, the credential configuration id and the one-time
	// pre-authorized_code, so the receiver needs nothing else to redeem it.
	CredentialOffer string `json:"credentialOffer"`
	// Message is a human-readable fallback for QERDS inboxes that surface the
	// body to an operator instead of auto-redeeming.
	Message string `json:"message,omitempty"`
}

// MarshalCredentialOfferEnvelope builds the QERDS body carrying offerURI.
func MarshalCredentialOfferEnvelope(senderOrgName, credentialName, offerURI string) (string, error) {
	env := CredentialOfferEnvelope{
		Type:            credentialOfferEnvelopeType,
		SenderOrgName:   senderOrgName,
		CredentialName:  credentialName,
		CredentialOffer: offerURI,
		Message: fmt.Sprintf(
			"%s has offered your organization a credential (%s). Your business wallet adds it automatically.",
			senderOrgName, credentialName),
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("attestation: marshal credential offer envelope: %w", err)
	}
	return string(b), nil
}

// ParseCredentialOfferEnvelope reports whether body is a credential-offer
// envelope and, if so, returns it. A body that is not JSON, carries a different
// Type, or has no offer is not one (ok=false) — so ordinary human QERDS messages
// pass through untouched.
func ParseCredentialOfferEnvelope(body string) (CredentialOfferEnvelope, bool) {
	var env CredentialOfferEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return CredentialOfferEnvelope{}, false
	}
	if env.Type != credentialOfferEnvelopeType || env.CredentialOffer == "" {
		return CredentialOfferEnvelope{}, false
	}
	return env, true
}
