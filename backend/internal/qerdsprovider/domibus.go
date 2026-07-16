package qerdsprovider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// DomibusProvider drives a Domibus AS4 access point through its WS plugin
// (SOAP). Domibus is the EU eDelivery reference access point; this makes our
// backend a requestor against it, exactly as the StubProvider is in-process.
//
// SCOPE: this targets the Domibus 4.x/5.x WS-plugin schema and the ebMS3
// namespaces below. The parties/service/action must match a PMode configured on
// the deployed Domibus (the defaults align with the Domibus sample PMode). It
// proves AS4 transport plumbing, NOT qualified compliance — see
// .ai/features/qerds.md. The live send/receive path depends on that PMode +
// keystore config and is not exercised by the default (stub) dev setup.
type DomibusProvider struct {
	endpoint string
	auth     RequestAuthenticator
	cfg      DomibusConfig
	http     *http.Client
}

// DomibusConfig is the ebMS3 addressing used on every submission. It must line
// up with a process in the deployed Domibus PMode.
type DomibusConfig struct {
	FromParty   string
	ToParty     string
	PartyType   string
	Service     string
	ServiceType string
	Action      string
}

const (
	soapNS    = "http://schemas.xmlsoap.org/soap/envelope/"
	ebmsNS    = "http://docs.oasis-open.org/ebxml-msg/ebms/v3.0/ns/core/200704/"
	backendNS = "http://org.ecodex.backend/1_1/"

	roleInitiator = ebmsNS + "initiator"
	roleResponder = ebmsNS + "responder"

	propOriginalSender = "originalSender"
	propFinalRecipient = "finalRecipient"
	payloadCID         = "cid:message"
	payloadMimeType    = "text/plain"
	attachmentCIDBase  = "cid:attachment-"

	domibusErrBodyLimit = 8 << 10
)

// NewDomibusProvider builds a Domibus WS-plugin client. endpoint is the backend
// service URL, e.g. http://domibus:8080/domibus/services/backend.
func NewDomibusProvider(endpoint string, auth RequestAuthenticator, cfg DomibusConfig, httpClient *http.Client) *DomibusProvider {
	return &DomibusProvider{
		endpoint: strings.TrimRight(endpoint, "/"),
		auth:     auth,
		cfg:      cfg,
		http:     httpClient,
	}
}

// Ping is the boot readiness probe: it fetches the WS-plugin WSDL, asserting the
// endpoint is reachable and serving. It does not assert the PMode is valid — a
// bad PMode surfaces on the first real submission.
func (p *DomibusProvider) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"?wsdl", nil)
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus ping request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus ping: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, domibusErrBodyLimit))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qerdsprovider: domibus ping: status %d", resp.StatusCode)
	}
	return nil
}

// ResolveAddress is the directory lookup. Address resolution is delegated to
// Domibus's PMode/SMP; here the identifier is used verbatim as the ebMS
// finalRecipient.
func (p *DomibusProvider) ResolveAddress(_ context.Context, identifier string) (Address, error) {
	return Address(identifier), nil
}

// Send submits a message via the WS-plugin submitMessage operation and returns
// the Domibus-assigned message id as the provider ref.
func (p *DomibusProvider) Send(ctx context.Context, msg OutboundMessage) (SendReceipt, error) {
	envelope := p.buildSubmitEnvelope(msg)
	var out submitResponse
	if err := p.call(ctx, "submitMessage", envelope, &out); err != nil {
		return SendReceipt{}, err
	}
	ref := strings.TrimSpace(out.MessageID)
	if ref == "" {
		return SendReceipt{}, fmt.Errorf("qerdsprovider: domibus submit: empty message id")
	}
	// Domibus acknowledges submission synchronously; delivery evidence arrives
	// asynchronously and is reconciled by a later poll/webhook.
	return SendReceipt{ProviderRef: ref, Status: StatusSubmitted}, nil
}

// Fetch pulls pending inbound messages for a single address. The WS-plugin
// queue is shared across the whole access point, so it MUST be scoped by
// finalRecipient: listPendingMessages is filtered to addr (otherwise every
// address would drain the whole queue, since retrieveMessage acknowledges and
// consumes each message), and each retrieved message is attributed to the
// finalRecipient carried in its own message properties, not to addr.
func (p *DomibusProvider) Fetch(ctx context.Context, addr Address) ([]InboundMessage, error) {
	var pending listPendingResponse
	if err := p.call(ctx, "listPendingMessages", newListPendingEnvelope(string(addr)), &pending); err != nil {
		return nil, err
	}

	messages := make([]InboundMessage, 0, len(pending.MessageIDs))
	for _, id := range pending.MessageIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		var retrieved retrieveResponse
		if err := p.call(ctx, "retrieveMessage", newRetrieveEnvelope(id), &retrieved); err != nil {
			return messages, err
		}
		messages = append(messages, retrieved.toInbound(id, addr))
	}
	return messages, nil
}

func (p *DomibusProvider) call(ctx context.Context, action string, payload any, out any) error {
	body, headers, err := p.auth.Authorize(payload)
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus authorize: %w", err)
	}

	raw, err := xml.Marshal(body)
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus marshal %s: %w", action, err)
	}
	doc := append([]byte(xml.Header), raw...)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus request %s: %w", action, err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `""`)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("qerdsprovider: domibus %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qerdsprovider: domibus %s: status %d: %s", action, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("qerdsprovider: domibus %s: decode response: %w", action, err)
	}
	return nil
}

func (p *DomibusProvider) buildSubmitEnvelope(msg OutboundMessage) submitEnvelope {
	// The body is always the first payload (cid:message); each attachment is an
	// additional ebMS3 payload part carried by its own content id.
	parts := []partInfo{{
		Href:       payloadCID,
		Properties: []property{{Name: "MimeType", Value: payloadMimeType}},
	}}
	payloads := []submitPayload{{
		// Empty Space resets the inherited backendNS to no namespace.
		XMLName:     xml.Name{Local: "payload"},
		PayloadID:   payloadCID,
		ContentType: payloadMimeType,
		Value: xmlValue{
			XMLName: xml.Name{Local: "value"},
			Value:   base64.StdEncoding.EncodeToString([]byte(msg.Body)),
		},
	}}
	for i, a := range msg.Attachments {
		cid := attachmentCIDBase + strconv.Itoa(i)
		mimeType := a.ContentType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		props := []property{{Name: "MimeType", Value: mimeType}}
		if a.Filename != "" {
			props = append(props, property{Name: "PayloadName", Value: a.Filename})
		}
		parts = append(parts, partInfo{Href: cid, Properties: props})
		payloads = append(payloads, submitPayload{
			XMLName:     xml.Name{Local: "payload"},
			PayloadID:   cid,
			ContentType: mimeType,
			Value: xmlValue{
				XMLName: xml.Name{Local: "value"},
				Value:   base64.StdEncoding.EncodeToString(a.Content),
			},
		})
	}

	return submitEnvelope{
		XMLName: xml.Name{Space: soapNS, Local: "Envelope"},
		Header: submitHeader{
			Messaging: messaging{
				XMLName: xml.Name{Space: ebmsNS, Local: "Messaging"},
				UserMessage: userMessage{
					PartyInfo: partyInfo{
						From: party{PartyID: partyID{Type: p.cfg.PartyType, Value: p.cfg.FromParty}, Role: roleInitiator},
						To:   party{PartyID: partyID{Type: p.cfg.PartyType, Value: p.cfg.ToParty}, Role: roleResponder},
					},
					CollaborationInfo: collaborationInfo{
						Service:        service{Type: p.cfg.ServiceType, Value: p.cfg.Service},
						Action:         p.cfg.Action,
						ConversationID: "1",
					},
					MessageProperties: []property{
						{Name: propOriginalSender, Value: string(msg.Sender)},
						{Name: propFinalRecipient, Value: string(msg.Recipient)},
					},
					PayloadInfo: payloadInfo{PartInfo: parts},
				},
			},
		},
		Body: submitBody{
			SubmitRequest: submitRequest{
				XMLName: xml.Name{Space: backendNS, Local: "submitRequest"},
				Payload: payloads,
			},
		},
	}
}

// --- SOAP request types (namespaced for the server) ---

type submitEnvelope struct {
	XMLName xml.Name
	Header  submitHeader `xml:"http://schemas.xmlsoap.org/soap/envelope/ Header"`
	Body    submitBody   `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
}

type submitHeader struct {
	Messaging messaging
}

type submitBody struct {
	SubmitRequest submitRequest
}

type messaging struct {
	XMLName     xml.Name
	UserMessage userMessage `xml:"UserMessage"`
}

type userMessage struct {
	PartyInfo         partyInfo         `xml:"PartyInfo"`
	CollaborationInfo collaborationInfo `xml:"CollaborationInfo"`
	MessageProperties []property        `xml:"MessageProperties>Property"`
	PayloadInfo       payloadInfo       `xml:"PayloadInfo"`
}

type partyInfo struct {
	From party `xml:"From"`
	To   party `xml:"To"`
}

type party struct {
	PartyID partyID `xml:"PartyId"`
	Role    string  `xml:"Role"`
}

type partyID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type collaborationInfo struct {
	Service        service `xml:"Service"`
	Action         string  `xml:"Action"`
	ConversationID string  `xml:"ConversationId"`
}

type service struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type property struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

type payloadInfo struct {
	PartInfo []partInfo `xml:"PartInfo"`
}

type partInfo struct {
	Href       string     `xml:"href,attr"`
	Properties []property `xml:"PartProperties>Property"`
}

type submitRequest struct {
	XMLName xml.Name
	Payload []submitPayload `xml:"payload"`
}

// submitPayload and its children are UNqualified (empty namespace) even though
// submitRequest sits in backendNS: the WS-plugin schema uses
// elementFormDefault="unqualified", so the payload/value elements must reset the
// default namespace (Domibus rejects them if they inherit backendNS).
type submitPayload struct {
	XMLName     xml.Name
	PayloadID   string   `xml:"payloadId,attr"`
	ContentType string   `xml:"contentType,attr"`
	Value       xmlValue `xml:"value"`
}

type xmlValue struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

func newListPendingEnvelope(finalRecipient string) listPendingEnvelope {
	req := listPendingRequest{XMLName: xml.Name{Space: backendNS, Local: "listPendingMessagesRequest"}}
	if finalRecipient != "" {
		// Unqualified (empty namespace), like the submitRequest payload — the
		// backend schema is elementFormDefault="unqualified".
		req.FinalRecipient = &xmlValue{XMLName: xml.Name{Local: "finalRecipient"}, Value: finalRecipient}
	}
	return listPendingEnvelope{
		XMLName: xml.Name{Space: soapNS, Local: "Envelope"},
		Body:    listPendingBody{Request: req},
	}
}

type listPendingEnvelope struct {
	XMLName xml.Name
	Body    listPendingBody `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
}

type listPendingBody struct {
	Request listPendingRequest
}

type listPendingRequest struct {
	XMLName xml.Name
	// Filters the shared queue to one recipient. Omitted (whole queue) when empty.
	FinalRecipient *xmlValue
}

func newRetrieveEnvelope(messageID string) retrieveEnvelope {
	return retrieveEnvelope{
		XMLName: xml.Name{Space: soapNS, Local: "Envelope"},
		Body: retrieveBody{
			Request: retrieveRequest{
				XMLName:   xml.Name{Space: backendNS, Local: "retrieveMessageRequest"},
				MessageID: messageID,
			},
		},
	}
}

type retrieveEnvelope struct {
	XMLName xml.Name
	Body    retrieveBody `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
}

type retrieveBody struct {
	Request retrieveRequest
}

type retrieveRequest struct {
	XMLName   xml.Name
	MessageID string `xml:"messageID"`
}

// --- SOAP response types (local-name matched, namespace-agnostic) ---

type submitResponse struct {
	MessageID string `xml:"Body>submitResponse>messageID"`
}

type listPendingResponse struct {
	MessageIDs []string `xml:"Body>listPendingMessagesResponse>messageID"`
}

type retrieveResponse struct {
	FromParty  string `xml:"Header>Messaging>UserMessage>PartyInfo>From>PartyId"`
	Properties []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:",chardata"`
	} `xml:"Header>Messaging>UserMessage>MessageProperties>Property"`
	Payload string `xml:"Body>retrieveMessageResponse>payload>value"`
}

// toInbound maps a retrieved message to an InboundMessage. The recipient comes
// from the message's own finalRecipient property; fallbackRecipient (the address
// that was polled) is used only when the property is absent, so a message is
// never misattributed to the poller.
func (r retrieveResponse) toInbound(messageID string, fallbackRecipient Address) InboundMessage {
	sender := r.FromParty
	recipient := fallbackRecipient
	subject := ""
	for _, prop := range r.Properties {
		switch prop.Name {
		case propOriginalSender:
			sender = prop.Value
		case propFinalRecipient:
			if prop.Value != "" {
				recipient = Address(prop.Value)
			}
		case "subject":
			subject = prop.Value
		}
	}
	body := ""
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Payload)); err == nil {
		body = string(decoded)
	}
	return InboundMessage{
		ProviderRef: messageID,
		Sender:      Address(sender),
		Recipient:   recipient,
		Subject:     subject,
		Body:        body,
	}
}
