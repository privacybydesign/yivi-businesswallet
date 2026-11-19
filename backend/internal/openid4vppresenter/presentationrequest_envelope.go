package openid4vppresenter

import (
	"encoding/json"
	"fmt"
)

// presentationRequestEnvelopeType discriminates a machine-consumable OpenID4VP
// Authorization Request carried in a QERDS message body from an ordinary human
// message. Bumped if the envelope shape changes so an older receiver ignores an
// incompatible one.
const presentationRequestEnvelopeType = "vp-presentation-request/v1"

// PresentationRequestEnvelope is the structured QERDS body that carries an
// OpenID4VP Authorization Request (pass-by-reference: client_id + request_uri)
// from one organization to another — the opposite direction of
// attestation.CredentialOfferEnvelope, and the transport issue #271 describes.
// The request travels as the message body, not an attachment; Receiver detects
// it by Type and queues it for the receiving organization to approve or decline
// (Service.Approve / Service.Decline). See .ai/features/oid4vp-over-qerds.md.
type PresentationRequestEnvelope struct {
	Type             string `json:"type"`
	SenderOrgName    string `json:"senderOrgName"`
	ClientID         string `json:"clientId"`
	RequestURI       string `json:"requestUri"`
	RequestURIMethod string `json:"requestUriMethod,omitempty"`
	// Message is a human-readable fallback for QERDS inboxes that surface the
	// body to an operator rather than parsing the envelope.
	Message string `json:"message,omitempty"`
}

// MarshalPresentationRequestEnvelope builds the QERDS body carrying an
// Authorization Request's invocation parameters — the same pass-by-reference
// shape Service.Start accepts from a browser.
func MarshalPresentationRequestEnvelope(senderOrgName, clientID, requestURI, requestURIMethod string) (string, error) {
	env := PresentationRequestEnvelope{
		Type:             presentationRequestEnvelopeType,
		SenderOrgName:    senderOrgName,
		ClientID:         clientID,
		RequestURI:       requestURI,
		RequestURIMethod: requestURIMethod,
		Message: fmt.Sprintf(
			"%s is requesting credentials from your organization. Review the request in your business wallet to approve or decline it.",
			senderOrgName),
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("openid4vppresenter: marshal presentation request envelope: %w", err)
	}
	return string(b), nil
}

// ParsePresentationRequestEnvelope reports whether body is a presentation-request
// envelope and, if so, returns it. A body that is not JSON, carries a different
// Type, or is missing client_id/request_uri is not one (ok=false) — so ordinary
// human QERDS messages pass through untouched.
func ParsePresentationRequestEnvelope(body string) (PresentationRequestEnvelope, bool) {
	var env PresentationRequestEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return PresentationRequestEnvelope{}, false
	}
	if env.Type != presentationRequestEnvelopeType || env.ClientID == "" || env.RequestURI == "" {
		return PresentationRequestEnvelope{}, false
	}
	return env, true
}
