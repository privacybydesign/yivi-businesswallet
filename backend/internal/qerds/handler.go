package qerds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Attachment limits. Payloads are stored inline as bytes (blob-column MVP), so
// these bound both the request and a single message's row footprint.
const (
	attachmentFormKey       = "attachments"
	maxAttachmentBytes      = 25 << 20 // 25 MiB per file
	maxAttachmentTotalBytes = 50 << 20 // 50 MiB per message
	maxAttachmentCount      = 20
	multipartMemoryBytes    = 8 << 20 // buffer in memory before spilling to a temp file
	multipartOverheadBytes  = 1 << 20 // headroom over the payload cap for framing + text fields
	defaultContentType      = "application/octet-stream"
)

type sender interface {
	Send(ctx context.Context, orgID uuid.UUID, from, recipient, subject, body string, attachments []qerdsprovider.Attachment) (Message, error)
	Poll(ctx context.Context, orgID uuid.UUID) (int, error)
	ReceiveInbound(ctx context.Context, in qerdsprovider.InboundMessage) error
}

type reader interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Message, error)
	GetWithEvidence(ctx context.Context, orgID, id uuid.UUID) (MessageWithEvidence, error)
	GetAttachmentContent(ctx context.Context, orgID, messageID, attachmentID uuid.UUID) (AttachmentContent, error)
}

type addressManager interface {
	ProvisionAddress(ctx context.Context, orgID uuid.UUID, address string, makeDefault bool, providerRef string) (Address, error)
	ListAddresses(ctx context.Context, orgID uuid.UUID) ([]Address, error)
	SetDefaultAddress(ctx context.Context, orgID, addressID uuid.UUID) (Address, error)
}

type contactManager interface {
	ListContacts(ctx context.Context, orgID uuid.UUID) ([]Contact, error)
	CreateContact(ctx context.Context, orgID uuid.UUID, in Contact) (Contact, error)
	DeleteContact(ctx context.Context, orgID, id uuid.UUID) error
}

// Handler serves the org-scoped QERDS API plus the machine-to-machine inbound
// webhook. Org routes compose the injected requireUser + authorize middleware
// (auth.RequireUser -> organization.Authorize); the webhook sits outside that
// chain and authenticates out-of-band.
type Handler struct {
	service       sender
	messages      reader
	addresses     addressManager
	contacts      contactManager
	requireUser   func(http.Handler) http.Handler
	authorize     func(http.Handler) http.Handler
	webhookSecret string
	addressDomain string
}

func NewHandler(service sender, messages reader, addresses addressManager, contacts contactManager, requireUser, authorize func(http.Handler) http.Handler, webhookSecret, addressDomain string) *Handler {
	return &Handler{
		service:       service,
		messages:      messages,
		addresses:     addresses,
		contacts:      contacts,
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
	mux.Handle("GET /orgs/{slug}/qerds/messages/{id}/attachments/{attachmentId}", orgScoped(respond.HandlerFunc(h.downloadAttachment)))
	mux.Handle("POST /orgs/{slug}/qerds/poll", orgScoped(respond.HandlerFunc(h.poll)))

	mux.Handle("GET /orgs/{slug}/qerds/addresses", orgScoped(respond.HandlerFunc(h.listAddresses)))
	provisionAddress := organization.RequirePermission(organization.ResourceQERDS, organization.ActionProvisionAddress)
	mux.Handle("POST /orgs/{slug}/qerds/addresses", orgScoped(provisionAddress(respond.HandlerFunc(h.provisionAddress))))
	mux.Handle("POST /orgs/{slug}/qerds/addresses/{id}/default", orgScoped(provisionAddress(respond.HandlerFunc(h.setDefaultAddress))))

	mux.Handle("GET /orgs/{slug}/qerds/contacts", orgScoped(respond.HandlerFunc(h.listContacts)))
	mux.Handle("POST /orgs/{slug}/qerds/contacts", orgScoped(respond.HandlerFunc(h.createContact)))
	mux.Handle("DELETE /orgs/{slug}/qerds/contacts/{id}", orgScoped(respond.HandlerFunc(h.deleteContact)))

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

// sendMessage accepts multipart/form-data: the text fields sender, recipient,
// subject and body plus zero or more file parts under the "attachments" key.
// Multipart (rather than JSON) keeps payload bytes off the base64 bloat path.
// An empty sender means the org default; a non-empty one must be an address the
// org owns.
func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) error {
	// Bound the whole request body before parsing so an oversized upload fails
	// fast, rather than buffering to memory and spilling to a temp file before
	// parseAttachments ever gets to enforce its limits. The headroom covers the
	// multipart framing and text fields around the attachment payload cap.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentTotalBytes+multipartOverheadBytes)

	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		return badRequest("invalid_body", "invalid multipart form")
	}

	sender := strings.TrimSpace(r.FormValue("sender"))
	recipient := strings.TrimSpace(r.FormValue("recipient"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	if recipient == "" {
		return badRequest("invalid_input", "recipient is required")
	}
	if subject == "" {
		return badRequest("invalid_input", "subject is required")
	}

	attachments, err := parseAttachments(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	msg, err := h.service.Send(r.Context(), org.ID, sender, recipient, subject, body, attachments)
	if errors.Is(err, ErrNoSenderAddress) {
		return &respond.APIError{Status: http.StatusConflict, Code: "no_sender_address", Message: "organization has no default digital address"}
	}
	if errors.Is(err, ErrSenderNotOwned) {
		return badRequest("invalid_sender", "sender is not one of your organization's addresses")
	}
	if err != nil {
		return fmt.Errorf("sending qerds message: %w", err)
	}

	respond.JSON(w, r, http.StatusAccepted, msg)
	return nil
}

// parseAttachments reads the uploaded file parts into provider attachments,
// enforcing the per-file, total and count limits. Content type comes from the
// part header (opaque — never sniffed), defaulting to octet-stream.
func parseAttachments(r *http.Request) ([]qerdsprovider.Attachment, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File[attachmentFormKey]
	if len(files) > maxAttachmentCount {
		return nil, badRequest("too_many_attachments", "too many attachments")
	}

	var total int64
	attachments := make([]qerdsprovider.Attachment, 0, len(files))
	for _, fh := range files {
		if fh.Size > maxAttachmentBytes {
			return nil, badRequest("attachment_too_large", fmt.Sprintf("attachment %q exceeds the size limit", fh.Filename))
		}
		total += fh.Size
		if total > maxAttachmentTotalBytes {
			return nil, badRequest("attachments_too_large", "attachments exceed the total size limit")
		}

		content, err := readMultipartFile(fh)
		if err != nil {
			return nil, err
		}

		contentType := fh.Header.Get("Content-Type")
		if contentType == "" {
			contentType = defaultContentType
		}
		filename := filepath.Base(strings.TrimSpace(fh.Filename))
		if filename == "" || filename == "." || filename == string(filepath.Separator) {
			filename = "attachment"
		}
		attachments = append(attachments, qerdsprovider.Attachment{
			Filename:    filename,
			ContentType: contentType,
			Content:     content,
		})
	}
	return attachments, nil
}

func readMultipartFile(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("opening attachment %q: %w", fh.Filename, err)
	}
	defer func() { _ = f.Close() }()

	// Bound the read defensively: fh.Size is advisory, so cap at one byte over
	// the limit and reject if exceeded rather than trusting the reported size.
	content, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading attachment %q: %w", fh.Filename, err)
	}
	if int64(len(content)) > maxAttachmentBytes {
		return nil, badRequest("attachment_too_large", fmt.Sprintf("attachment %q exceeds the size limit", fh.Filename))
	}
	return content, nil
}

func (h *Handler) downloadAttachment(w http.ResponseWriter, r *http.Request) error {
	messageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid message id")
	}
	attachmentID, err := uuid.Parse(r.PathValue("attachmentId"))
	if err != nil {
		return badRequest("invalid_id", "invalid attachment id")
	}

	org := organization.OrgFromContext(r.Context())
	att, err := h.messages.GetAttachmentContent(r.Context(), org.ID, messageID, attachmentID)
	if errors.Is(err, ErrAttachmentNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "attachment_not_found", Message: "attachment not found"}
	}
	if err != nil {
		return fmt.Errorf("getting qerds attachment: %w", err)
	}

	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(att.Content)))
	// Content is opaque and provider-supplied; never let the browser sniff it.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(att.Content)
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

// addressNamespaceSeparator delimits an organization's verified slug namespace
// from an optional subdivision in a QERDS local part (e.g. "acme.sales"). Slugs
// are [a-z0-9-] (organization.ValidateSlug) and never contain it, so a local
// part inside one org's namespace can never collide with another org's slug or
// namespace.
const addressNamespaceSeparator = "."

// namespaceSuffixPattern matches the part after "<slug><separator>": one or more
// slug-shaped labels joined by the separator (e.g. "sales", "sales.eu"). It
// mirrors organization.ValidateSlug's per-label grammar.
var namespaceSuffixPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*(?:\.[a-z0-9]+(?:-[a-z0-9]+)*)*$`)

// namespacedLocalPart resolves the local part an org may provision from the
// admin-supplied input, constraining it to the org's own verified namespace. An
// org's verified claim is its slug (KVK-validated and unique per deployment at
// bootstrap); it owns the local part equal to that slug plus any subdivision
// beneath it ("acme", "acme.sales"). An empty input defaults to the bare slug.
// Anything outside the namespace is rejected with ErrAddressOutsideNamespace,
// closing the cross-org squatting hole: one org can never provision a local
// part that belongs to (or collides with) another org's slug or namespace.
func namespacedLocalPart(slug, input string) (string, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || input == slug {
		return slug, nil
	}
	suffix, ok := strings.CutPrefix(input, slug+addressNamespaceSeparator)
	if !ok || !namespaceSuffixPattern.MatchString(suffix) {
		return "", ErrAddressOutsideNamespace
	}
	return input, nil
}

func (h *Handler) provisionAddress(w http.ResponseWriter, r *http.Request) error {
	var req provisionAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := organization.OrgFromContext(r.Context())
	localPart, err := namespacedLocalPart(org.Slug, req.LocalPart)
	if errors.Is(err, ErrAddressOutsideNamespace) {
		return badRequest("address_outside_namespace", "digital address must be within your organization's namespace")
	}
	if err != nil {
		return fmt.Errorf("resolving qerds address namespace: %w", err)
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

func (h *Handler) setDefaultAddress(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid address id")
	}

	org := organization.OrgFromContext(r.Context())
	addr, err := h.addresses.SetDefaultAddress(r.Context(), org.ID, id)
	if errors.Is(err, ErrAddressNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "address_not_found", Message: "digital address not found"}
	}
	if err != nil {
		return fmt.Errorf("setting default qerds address: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, addr)
	return nil
}

func (h *Handler) listContacts(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	contacts, err := h.contacts.ListContacts(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing qerds contacts: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, contacts)
	return nil
}

type createContactRequest struct {
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	LegalName *string `json:"legalName"`
	KVKNumber *string `json:"kvkNumber"`
	EUID      *string `json:"euid"`
}

// trimOptional trims an optional string field, collapsing an empty result to nil so
// blank inputs are stored as SQL NULL rather than "".
func trimOptional(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (h *Handler) createContact(w http.ResponseWriter, r *http.Request) error {
	var req createContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Address = strings.TrimSpace(req.Address)
	if req.Name == "" {
		return badRequest("invalid_input", "name is required")
	}
	if req.Address == "" {
		return badRequest("invalid_input", "address is required")
	}

	org := organization.OrgFromContext(r.Context())
	contact, err := h.contacts.CreateContact(r.Context(), org.ID, Contact{
		Name:      req.Name,
		Address:   req.Address,
		LegalName: trimOptional(req.LegalName),
		KVKNumber: trimOptional(req.KVKNumber),
		EUID:      trimOptional(req.EUID),
	})
	if errors.Is(err, ErrContactAddressTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "address_taken", Message: "a contact with that address already exists"}
	}
	if err != nil {
		return fmt.Errorf("creating qerds contact: %w", err)
	}

	respond.JSON(w, r, http.StatusCreated, contact)
	return nil
}

func (h *Handler) deleteContact(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid contact id")
	}

	org := organization.OrgFromContext(r.Context())
	if err := h.contacts.DeleteContact(r.Context(), org.ID, id); errors.Is(err, ErrContactNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "contact_not_found", Message: "contact not found"}
	} else if err != nil {
		return fmt.Errorf("deleting qerds contact: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
