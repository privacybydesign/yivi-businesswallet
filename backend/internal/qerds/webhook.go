package qerds

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// signatureHeader carries the hex-encoded HMAC-SHA256 of the raw request body.
const signatureHeader = "X-QERDS-Signature"

// webhookBodyLimit caps the inbound webhook body to bound memory on a hostile
// or misbehaving caller.
const webhookBodyLimit = 1 << 20 // 1 MiB

type inboundEvidence struct {
	Type               string    `json:"type"`
	ProviderRef        string    `json:"providerRef"`
	QualifiedTimestamp time.Time `json:"qualifiedTimestamp"`
	Raw                []byte    `json:"raw"`
}

type inboundPayload struct {
	ProviderRef string            `json:"providerRef"`
	Sender      string            `json:"sender"`
	Recipient   string            `json:"recipient"`
	Subject     string            `json:"subject"`
	Body        string            `json:"body"`
	Evidence    []inboundEvidence `json:"evidence"`
}

func (p inboundPayload) toMessage() qerdsprovider.InboundMessage {
	evidence := make([]qerdsprovider.Evidence, 0, len(p.Evidence))
	for _, e := range p.Evidence {
		evidence = append(evidence, qerdsprovider.Evidence{
			Type:               e.Type,
			ProviderRef:        e.ProviderRef,
			QualifiedTimestamp: e.QualifiedTimestamp,
			Raw:                e.Raw,
		})
	}
	return qerdsprovider.InboundMessage{
		ProviderRef: p.ProviderRef,
		Sender:      qerdsprovider.Address(p.Sender),
		Recipient:   qerdsprovider.Address(p.Recipient),
		Subject:     p.Subject,
		Body:        p.Body,
		Evidence:    evidence,
	}
}

// webhook receives a message pushed by the provider. It is authenticated by an
// HMAC signature over the raw body, not a session — this is a machine-to-machine
// seam. When no secret is configured the endpoint is disabled (404).
func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) error {
	if h.webhookSecret == "" {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "not found"}
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, webhookBodyLimit))
	if err != nil {
		return fmt.Errorf("reading qerds webhook body: %w", err)
	}

	if !h.validSignature(raw, r.Header.Get(signatureHeader)) {
		return &respond.APIError{Status: http.StatusUnauthorized, Code: "invalid_signature", Message: "invalid signature"}
	}

	var payload inboundPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if payload.ProviderRef == "" || payload.Recipient == "" {
		return badRequest("invalid_input", "providerRef and recipient are required")
	}

	if err := h.service.ReceiveInbound(r.Context(), payload.toMessage()); errors.Is(err, ErrAddressNotFound) {
		// Unknown recipient address — accepted-but-dropped so the provider does
		// not retry forever; logged for investigation.
		slog.WarnContext(r.Context(), "qerds webhook for unknown recipient",
			slog.String("recipient", payload.Recipient))
	} else if err != nil {
		return fmt.Errorf("receiving qerds inbound: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) validSignature(body []byte, provided string) bool {
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(provided))
}
