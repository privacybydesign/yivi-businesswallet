package qerds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type sender interface {
	Send(ctx context.Context, orgID uuid.UUID, recipient, subject, body string) (Message, error)
	Poll(ctx context.Context, orgID uuid.UUID) (int, error)
	ReceiveInbound(ctx context.Context, in qerdsprovider.InboundMessage) error
}

type reader interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Message, error)
	GetWithEvidence(ctx context.Context, orgID, id uuid.UUID) (MessageWithEvidence, error)
}

type addressManager interface {
	ProvisionAddress(ctx context.Context, orgID uuid.UUID, address string, makeDefault bool, providerRef string) (Address, error)
	ListAddresses(ctx context.Context, orgID uuid.UUID) ([]Address, error)
}

// Handler serves the org-scoped QERDS API plus the machine-to-machine inbound
// webhook. Org routes compose the injected requireUser + authorize middleware
// (auth.RequireUser -> organization.Authorize); the webhook sits outside that
// chain and authenticates out-of-band.
type Handler struct {
	service       sender
	messages      reader
	addresses     addressManager
	requireUser   func(http.Handler) http.Handler
	authorize     func(http.Handler) http.Handler
	webhookSecret string
	addressDomain string
}

func NewHandler(service sender, messages reader, addresses addressManager, requireUser, authorize func(http.Handler) http.Handler, webhookSecret, addressDomain string) *Handler {
	return &Handler{
		service:       service,
		messages:      messages,
		addresses:     addresses,
		requireUser:   requireUser,
		authorize:     authorize,
		webhookSecret: webhookSecret,
		addressDomain: addressDomain,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}

	mux.Handle("GET /orgs/{slug}/qerds/messages", orgScoped(respond.HandlerFunc(h.listMessages)))
	mux.Handle("POST /orgs/{slug}/qerds/messages", orgScoped(respond.HandlerFunc(h.sendMessage)))
	mux.Handle("GET /orgs/{slug}/qerds/messages/{id}", orgScoped(respond.HandlerFunc(h.getMessage)))
	mux.Handle("POST /orgs/{slug}/qerds/poll", orgScoped(respond.HandlerFunc(h.poll)))

	mux.Handle("GET /orgs/{slug}/qerds/addresses", orgScoped(respond.HandlerFunc(h.listAddresses)))
	mux.Handle("POST /orgs/{slug}/qerds/addresses", orgScoped(organization.RequireOrgAdmin(respond.HandlerFunc(h.provisionAddress))))

	// Machine-to-machine inbound push (Art 6(1)(d)); no session, out-of-band auth.
	mux.Handle("POST /qerds/webhook", respond.HandlerFunc(h.webhook))
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	messages, err := h.messages.List(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing qerds messages: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, messages)
	return nil
}

type sendMessageRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) error {
	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Recipient = strings.TrimSpace(req.Recipient)
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Recipient == "" {
		return badRequest("invalid_input", "recipient is required")
	}
	if req.Subject == "" {
		return badRequest("invalid_input", "subject is required")
	}

	org := organization.OrgFromContext(r.Context())
	msg, err := h.service.Send(r.Context(), org.ID, req.Recipient, req.Subject, req.Body)
	if errors.Is(err, ErrNoSenderAddress) {
		return &respond.APIError{Status: http.StatusConflict, Code: "no_sender_address", Message: "organization has no default digital address"}
	}
	if err != nil {
		return fmt.Errorf("sending qerds message: %w", err)
	}

	respond.JSON(w, r, http.StatusAccepted, msg)
	return nil
}

func (h *Handler) getMessage(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid message id")
	}

	org := organization.OrgFromContext(r.Context())
	msg, err := h.messages.GetWithEvidence(r.Context(), org.ID, id)
	if errors.Is(err, ErrMessageNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "message_not_found", Message: "message not found"}
	}
	if err != nil {
		return fmt.Errorf("getting qerds message: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, msg)
	return nil
}

func (h *Handler) poll(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	received, err := h.service.Poll(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("polling qerds inbound: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, map[string]int{"received": received})
	return nil
}

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	addresses, err := h.addresses.ListAddresses(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing qerds addresses: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, addresses)
	return nil
}

type provisionAddressRequest struct {
	LocalPart string `json:"localPart"`
	Default   bool   `json:"default"`
}

func (h *Handler) provisionAddress(w http.ResponseWriter, r *http.Request) error {
	var req provisionAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := organization.OrgFromContext(r.Context())
	localPart := strings.TrimSpace(req.LocalPart)
	if localPart == "" {
		localPart = org.Slug
	}
	address := localPart + "@" + h.addressDomain

	// Guarantee a sending address exists: the first address is always default.
	existing, err := h.addresses.ListAddresses(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("checking existing qerds addresses: %w", err)
	}
	makeDefault := req.Default || len(existing) == 0

	addr, err := h.addresses.ProvisionAddress(r.Context(), org.ID, address, makeDefault, "")
	if errors.Is(err, ErrAddressTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "address_taken", Message: "digital address already taken"}
	}
	if err != nil {
		return fmt.Errorf("provisioning qerds address: %w", err)
	}

	respond.JSON(w, r, http.StatusCreated, addr)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
