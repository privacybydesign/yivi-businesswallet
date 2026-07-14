package qerdsprovider

import (
	"encoding/base64"
	"encoding/xml"
	"strings"
	"testing"
)

func testDomibus() *DomibusProvider {
	return NewDomibusProvider(
		"http://domibus:8080/domibus/services/backend",
		NewTokenAuthenticator(""),
		DomibusConfig{
			FromParty:   "domibus-blue",
			ToParty:     "domibus-red",
			PartyType:   "urn:oasis:names:tc:ebcore:partyid-type:unregistered",
			Service:     "bdx:noprocess",
			ServiceType: "tc1",
			Action:      "TC1Leg1",
		},
		nil,
	)
}

func TestDomibusBuildSubmitEnvelope(t *testing.T) {
	p := testDomibus()
	envelope := p.buildSubmitEnvelope(OutboundMessage{
		Sender:    "alice@qerds.localhost",
		Recipient: "bob@qerds.localhost",
		Subject:   "hello",
		Body:      "world",
	})

	raw, err := xml.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	doc := string(raw)

	for _, want := range []string{
		soapNS,
		ebmsNS,
		backendNS,
		"domibus-blue",
		"domibus-red",
		"bdx:noprocess",
		"TC1Leg1",
		"alice@qerds.localhost",
		"bob@qerds.localhost",
		propOriginalSender,
		propFinalRecipient,
		payloadCID,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("submit envelope missing %q\n%s", want, doc)
		}
	}

	// The body is base64-encoded into the payload value.
	if !strings.Contains(doc, base64.StdEncoding.EncodeToString([]byte("world"))) {
		t.Errorf("submit envelope missing base64 payload\n%s", doc)
	}

	// Regression: the WS-plugin submitRequest payload must be UNqualified. It
	// sits inside backendNS, so it has to reset the default namespace to empty —
	// live Domibus rejects it (EBMS unmarshalling error) otherwise.
	if !strings.Contains(doc, `xmlns=""`) {
		t.Errorf("payload must reset to the empty namespace (xmlns=\"\")\n%s", doc)
	}
}

func TestDomibusParseSubmitResponse(t *testing.T) {
	const body = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <ns:submitResponse xmlns:ns="http://org.ecodex.backend/1_1/">
      <ns:messageID>abc-123@domibus.eu</ns:messageID>
    </ns:submitResponse>
  </soap:Body>
</soap:Envelope>`

	var out submitResponse
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MessageID != "abc-123@domibus.eu" {
		t.Fatalf("messageID = %q, want %q", out.MessageID, "abc-123@domibus.eu")
	}
}

func TestDomibusParseListPending(t *testing.T) {
	const body = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <ns:listPendingMessagesResponse xmlns:ns="http://org.ecodex.backend/1_1/">
      <ns:messageID>m1@domibus.eu</ns:messageID>
      <ns:messageID>m2@domibus.eu</ns:messageID>
    </ns:listPendingMessagesResponse>
  </soap:Body>
</soap:Envelope>`

	var out listPendingResponse
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.MessageIDs) != 2 || out.MessageIDs[0] != "m1@domibus.eu" {
		t.Fatalf("messageIDs = %v, want [m1 m2]", out.MessageIDs)
	}
}

func TestDomibusRetrieveToInbound(t *testing.T) {
	body := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Header>
    <eb:Messaging xmlns:eb="http://docs.oasis-open.org/ebxml-msg/ebms/v3.0/ns/core/200704/">
      <eb:UserMessage>
        <eb:PartyInfo><eb:From><eb:PartyId>domibus-red</eb:PartyId></eb:From></eb:PartyInfo>
        <eb:MessageProperties>
          <eb:Property name="originalSender">alice@qerds.localhost</eb:Property>
          <eb:Property name="subject">Quarterly filing</eb:Property>
        </eb:MessageProperties>
      </eb:UserMessage>
    </eb:Messaging>
  </soap:Header>
  <soap:Body>
    <ns:retrieveMessageResponse xmlns:ns="http://org.ecodex.backend/1_1/">
      <ns:payload><ns:value>` + base64.StdEncoding.EncodeToString([]byte("body text")) + `</ns:value></ns:payload>
    </ns:retrieveMessageResponse>
  </soap:Body>
</soap:Envelope>`

	var out retrieveResponse
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	inbound := out.toInbound("m1@domibus.eu", "bob@qerds.localhost")
	if inbound.ProviderRef != "m1@domibus.eu" {
		t.Errorf("providerRef = %q", inbound.ProviderRef)
	}
	if inbound.Sender != "alice@qerds.localhost" {
		t.Errorf("sender = %q, want originalSender property", inbound.Sender)
	}
	if inbound.Subject != "Quarterly filing" {
		t.Errorf("subject = %q", inbound.Subject)
	}
	if inbound.Body != "body text" {
		t.Errorf("body = %q", inbound.Body)
	}
	if inbound.Recipient != "bob@qerds.localhost" {
		t.Errorf("recipient = %q", inbound.Recipient)
	}
}
