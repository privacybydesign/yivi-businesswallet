package openid4vprequester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	// maxSendBody caps a send request: an address and a few credential types
	// with their claim names.
	maxSendBody = 64 << 10
	// maxResponseBody caps a direct_post answer; SD-JWT VCs with their
	// disclosures run to a few kilobytes each.
	maxResponseBody = 4 << 20
	// requestObjectType is the JAR media type (RFC 9101).
	requestObjectType = "application/oauth-authz-req+jwt"
	// msgRequestNotFound answers an unknown or malformed request id alike.
	msgRequestNotFound = "unknown presentation request"
)

// credentialCatalog lists the credential types issuers on this deployment
// designed — the trust scheme's catalogue an admin picks a request from.
type credentialCatalog interface {
	ListSchemaCatalog(ctx context.Context) ([]attestation.CatalogEntry, error)
}

// Handler serves two audiences. An organization's admins send and read their
// outbound requests on the tenant seam. The receiving wallet — software, with
// no session — fetches the Request Object and posts its answer on the public
// routes, authenticated by the request's one-time fetch and its state.
type Handler struct {
	svc         *Service
	catalog     credentialCatalog
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(svc *Service, catalog credentialCatalog, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{svc: svc, catalog: catalog, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("POST /orgs/{slug}/openid4vp/outbound", admin(respond.HandlerFunc(h.send)))
	mux.Handle("GET /orgs/{slug}/openid4vp/outbound", admin(respond.HandlerFunc(h.list)))
	mux.Handle("GET /orgs/{slug}/openid4vp/outbound/{id}", admin(respond.HandlerFunc(h.get)))
	mux.Handle("GET /orgs/{slug}/openid4vp/credential-types", admin(respond.HandlerFunc(h.credentialTypes)))

	mux.Handle("GET /openid4vp/outbound/{id}/request-object", respond.HandlerFunc(h.requestObject))
	mux.Handle("POST /openid4vp/outbound/{id}/response", respond.HandlerFunc(h.response))
}

type sendCredential struct {
	VCT    string   `json:"vct"`
	Claims []string `json:"claims"`
}

type sendRequest struct {
	From        string           `json:"from"`
	Recipient   string           `json:"recipient"`
	Credentials []sendCredential `json:"credentials"`
}

// requestView is what an admin sees of a request: never the nonce, state or
// signed Request Object.
type requestView struct {
	ID            string                `json:"id"`
	Sender        string                `json:"sender"`
	Recipient     string                `json:"recipient"`
	Status        string                `json:"status"`
	FailureReason string                `json:"failureReason,omitempty"`
	Fetched       bool                  `json:"fetched"`
	Credentials   []CredentialRequest   `json:"credentials"`
	Disclosed     []DisclosedCredential `json:"disclosed"`
	CreatedAt     time.Time             `json:"createdAt"`
	ExpiresAt     time.Time             `json:"expiresAt"`
	RespondedAt   *time.Time            `json:"respondedAt,omitempty"`
}

func (h *Handler) view(r Request) requestView {
	v := requestView{
		ID:          r.ID.String(),
		Sender:      r.SenderAddress,
		Recipient:   r.RecipientAddress,
		Status:      r.EffectiveStatus(h.svc.Now()),
		Fetched:     r.RequestFetchedAt != nil,
		Credentials: r.Credentials,
		Disclosed:   r.Disclosed,
		CreatedAt:   r.CreatedAt,
		ExpiresAt:   r.ExpiresAt,
		RespondedAt: r.RespondedAt,
	}
	if r.FailureReason != nil {
		v.FailureReason = *r.FailureReason
	}
	if v.Disclosed == nil {
		v.Disclosed = []DisclosedCredential{}
	}
	return v
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) error {
	var req sendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSendBody)).Decode(&req); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid JSON body"}
	}
	creds := make([]CredentialRequest, 0, len(req.Credentials))
	for _, c := range req.Credentials {
		creds = append(creds, CredentialRequest{VCT: c.VCT, Claims: c.Claims})
	}
	u := auth.UserFromContext(r.Context())
	org := organization.OrgFromContext(r.Context())
	out, err := h.svc.Send(r.Context(), org, u.ID, SendInput{From: req.From, Recipient: req.Recipient, Credentials: creds})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, h.view(out))
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	reqs, err := h.svc.List(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing outbound presentation requests: %w", err)
	}
	out := make([]requestView, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, h.view(req))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	req, err := h.svc.Get(r.Context(), org.ID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, h.view(req))
	return nil
}

// credentialTypeView is one entry of the catalogue: what to ask for (vct and
// attribute keys) and how to show it (names, labels, issuer).
type credentialTypeView struct {
	VCT        string                    `json:"vct"`
	Name       string                    `json:"name"`
	Issuer     string                    `json:"issuer"`
	Attributes []credentialTypeAttribute `json:"attributes"`
}

type credentialTypeAttribute struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

func (h *Handler) credentialTypes(w http.ResponseWriter, r *http.Request) error {
	entries, err := h.catalog.ListSchemaCatalog(r.Context())
	if err != nil {
		return fmt.Errorf("listing the credential catalogue: %w", err)
	}
	out := make([]credentialTypeView, 0, len(entries))
	for _, e := range entries {
		attrs := make([]credentialTypeAttribute, 0, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs = append(attrs, credentialTypeAttribute{Key: a.Key, Label: a.Label})
		}
		out = append(out, credentialTypeView{VCT: e.VCT, Name: e.DisplayName, Issuer: e.IssuerName, Attributes: attrs})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) requestObject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	jar, err := h.svc.RequestObject(r.Context(), id)
	if err != nil {
		return mapError(err)
	}
	w.Header().Set("Content-Type", requestObjectType)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte(jar)); err != nil {
		slog.ErrorContext(r.Context(), "write request object", slog.String("error", err.Error()))
	}
	return nil
}

// response is the OpenID4VP response_uri (direct_post). It answers with an
// empty JSON object: no redirect_uri, since no browser is waiting on either side.
func (h *Handler) response(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxResponseBody)
	if err := r.ParseForm(); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid form body"}
	}
	if err := h.svc.Respond(r.Context(), id, r.PostForm); err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, struct{}{})
	return nil
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, &respond.APIError{Status: http.StatusNotFound, Code: ErrNotFound.Error(), Message: msgRequestNotFound}
	}
	return id, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidInput.Error(), Message: err.Error()}
	case errors.Is(err, qerds.ErrSenderNotOwned):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_sender", Message: "sender is not one of your organization's addresses"}
	case errors.Is(err, qerds.ErrNoSenderAddress):
		return &respond.APIError{Status: http.StatusConflict, Code: "no_sender_address", Message: "organization has no default digital address"}
	case errors.Is(err, ErrDeliveryFailed):
		return &respond.APIError{Status: http.StatusBadGateway, Code: ErrDeliveryFailed.Error(), Message: "the request could not be sent to the recipient"}
	case errors.Is(err, ErrNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: ErrNotFound.Error(), Message: msgRequestNotFound}
	case errors.Is(err, ErrNotPending):
		return &respond.APIError{Status: http.StatusConflict, Code: ErrNotPending.Error(), Message: "this presentation request has already been answered or has expired"}
	case errors.Is(err, ErrInvalidResponse):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidResponse.Error(), Message: err.Error()}
	default:
		return fmt.Errorf("openid4vprequester: %w", err)
	}
}
