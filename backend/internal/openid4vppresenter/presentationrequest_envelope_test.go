package openid4vppresenter

import "testing"

func TestMarshalParsePresentationRequestEnvelopeRoundTrips(t *testing.T) {
	body, err := MarshalPresentationRequestEnvelope("Acme", "x509_san_dns:acme.example.com", "https://acme.example.com/requests/1", "get")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	env, ok := ParsePresentationRequestEnvelope(body)
	if !ok {
		t.Fatal("expected envelope to parse")
	}
	if env.SenderOrgName != "Acme" || env.ClientID != "x509_san_dns:acme.example.com" ||
		env.RequestURI != "https://acme.example.com/requests/1" || env.RequestURIMethod != "get" {
		t.Errorf("round trip lost fields: %+v", env)
	}
	if env.Message == "" {
		t.Error("expected a human-readable fallback message")
	}
}

func TestParsePresentationRequestEnvelopeIgnoresOtherMessages(t *testing.T) {
	cases := []string{
		"just a human message",
		`{"type":"eaa-credential-offer/v1","credentialOffer":"openid-credential-offer://?x=1"}`,
		`{"type":"vp-presentation-request/v1"}`,                         // missing clientId/requestUri
		`{"type":"vp-presentation-request/v1","clientId":"x"}`,          // missing requestUri
		`{"type":"vp-presentation-request/v1","requestUri":"https://"}`, // missing clientId
	}
	for _, body := range cases {
		if _, ok := ParsePresentationRequestEnvelope(body); ok {
			t.Errorf("body %q should not parse as a presentation request", body)
		}
	}
}
