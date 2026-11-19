package signing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The signing API: an organisation's own software, authenticated by one of the
// org's API keys (the same keys the identity proofing API takes), asks people to
// sign a document and reads how that went. It is how signing is embedded in
// another system: that system makes the request, sends each signee to the signing
// page (or lets the wallet mail them), and fetches the signed PDF when done.

// APICaller is who an authenticated API key acts for.
type APICaller struct {
	OrgID uuid.UUID
	// Slug is the org's, for the links in a member signer's mail.
	Slug  string
	KeyID uuid.UUID
}

// APIAuth authenticates the signing API's callers. The adapter in cmd/api puts
// the org's API keys behind it, so this slice knows neither how a key is stored
// nor what its scopes are called.
type APIAuth interface {
	// RequireRead and RequireWrite let a request through only with a key that
	// may read, or make, signing requests.
	RequireRead(next respond.HandlerFunc) http.Handler
	RequireWrite(next respond.HandlerFunc) http.Handler
	// Caller returns the key a request authenticated with.
	Caller(ctx context.Context) (APICaller, error)
}

type apiService interface {
	CreateAPIRequest(ctx context.Context, orgID, apiKeyID uuid.UUID, slug string, in APIRequestInput) (Request, map[uuid.UUID]string, error)
	GetAPIRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	ListAPIRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error)
	GetAPIDocument(ctx context.Context, orgID, id uuid.UUID) ([]byte, string, error)
	IssueSignerLink(ctx context.Context, orgID, requestID, signerID uuid.UUID) (string, error)
}

// APIHandler serves the signing API.
type APIHandler struct {
	svc  apiService
	auth APIAuth
}

func NewAPIHandler(svc apiService, auth APIAuth) *APIHandler {
	return &APIHandler{svc: svc, auth: auth}
}

func (h *APIHandler) Register(mux *http.ServeMux) {
	mux.Handle("POST /signing/requests", h.auth.RequireWrite(h.create))
	mux.Handle("GET /signing/requests", h.auth.RequireRead(h.list))
	mux.Handle("GET /signing/requests/{id}", h.auth.RequireRead(h.get))
	mux.Handle("GET /signing/requests/{id}/document", h.auth.RequireRead(h.document))
	mux.Handle("POST /signing/requests/{id}/signers/{signerID}/link", h.auth.RequireWrite(h.signerLink))
}

// apiSigner is one signee as the API shows them.
type apiSigner struct {
	ID            uuid.UUID   `json:"id"`
	Kind          string      `json:"kind"`
	UserID        *uuid.UUID  `json:"userId,omitempty"`
	Name          string      `json:"name"`
	Email         string      `json:"email"`
	Order         int         `json:"order"`
	Status        string      `json:"status"`
	Placements    []Placement `json:"placements"`
	SignedAt      *time.Time  `json:"signedAt,omitempty"`
	DeclinedAt    *time.Time  `json:"declinedAt,omitempty"`
	DeclineReason string      `json:"declineReason,omitempty"`
	// SigningURL is the signee's signing page: present on the answer that made
	// the link, and only then, since the link is kept nowhere but with them.
	SigningURL string `json:"signingUrl,omitempty"`
}

// apiRequest is a signing request as the API shows it.
type apiRequest struct {
	ID               uuid.UUID   `json:"id"`
	Status           string      `json:"status"`
	Filename         string      `json:"filename"`
	Mode             string      `json:"mode"`
	ReturnURL        string      `json:"returnUrl,omitempty"`
	RecipientChannel string      `json:"recipientChannel"`
	RecipientName    string      `json:"recipientName,omitempty"`
	RecipientAddress string      `json:"recipientAddress,omitempty"`
	DeliveryStatus   string      `json:"deliveryStatus"`
	DeliveryError    string      `json:"deliveryError,omitempty"`
	Signers          []apiSigner `json:"signers"`
	CreatedAt        time.Time   `json:"createdAt"`
	CompletedAt      *time.Time  `json:"completedAt,omitempty"`
}

func newAPIRequest(req Request, links map[uuid.UUID]string) apiRequest {
	out := apiRequest{
		ID: req.ID, Status: req.Status, Filename: req.Filename, Mode: req.Mode, ReturnURL: req.ReturnURL,
		RecipientChannel: req.RecipientChannel, RecipientName: req.RecipientName,
		RecipientAddress: req.RecipientAddress, DeliveryStatus: req.DeliveryStatus,
		DeliveryError: req.DeliveryError, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		Signers: make([]apiSigner, 0, len(req.Signers)),
	}
	for _, sg := range req.Signers {
		placements := sg.Placements
		if placements == nil {
			placements = []Placement{}
		}
		out.Signers = append(out.Signers, apiSigner{
			ID: sg.ID, Kind: sg.Kind, UserID: sg.UserID, Name: sg.Name, Email: sg.Email,
			Order: sg.Order, Status: sg.Status, Placements: placements, SignedAt: sg.SignedAt,
			DeclinedAt: sg.DeclinedAt, DeclineReason: sg.DeclineReason, SigningURL: links[sg.ID],
		})
	}
	return out
}

// Form fields the API's create takes on top of the wallet's own.
const (
	formSendMail  = "sendMail"
	formReturnURL = "returnUrl"
)

func (h *APIHandler) create(w http.ResponseWriter, r *http.Request) error {
	caller, err := h.auth.Caller(r.Context())
	if err != nil {
		return err
	}
	upload, err := parseUpload(w, r)
	if err != nil {
		return err
	}
	// Mail is the default, as it is in the wallet: only an explicit false turns
	// it off, so a caller that names no preference does not leave signees unasked.
	sendMail := true
	if raw := strings.TrimSpace(r.FormValue(formSendMail)); raw != "" {
		sendMail, err = strconv.ParseBool(raw)
		if err != nil {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "sendMail must be true or false"}
		}
	}
	req, links, err := h.svc.CreateAPIRequest(r.Context(), caller.OrgID, caller.KeyID, caller.Slug, APIRequestInput{
		Filename: upload.filename, PDF: upload.pdf, Signers: upload.signers, Mode: upload.mode,
		Recipient: upload.recipient, SendMail: sendMail, ReturnURL: r.FormValue(formReturnURL),
	})
	if err := mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusCreated, newAPIRequest(req, links))
	return nil
}

type apiRequestList struct {
	Requests   []apiRequest `json:"requests"`
	NextCursor string       `json:"nextCursor"`
}

func (h *APIHandler) list(w http.ResponseWriter, r *http.Request) error {
	caller, err := h.auth.Caller(r.Context())
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	reqs, next, err := h.svc.ListAPIRequests(r.Context(), caller.OrgID, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, ErrInvalidRequest) {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "invalid pagination cursor"}
	}
	if err != nil {
		return fmt.Errorf("listing signing requests: %w", err)
	}
	out := apiRequestList{Requests: make([]apiRequest, 0, len(reqs)), NextCursor: next}
	for _, req := range reqs {
		out.Requests = append(out.Requests, newAPIRequest(req, nil))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *APIHandler) get(w http.ResponseWriter, r *http.Request) error {
	caller, id, err := h.callerAndRequest(r)
	if err != nil {
		return err
	}
	req, err := h.svc.GetAPIRequest(r.Context(), caller.OrgID, id)
	if err := mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, newAPIRequest(req, nil))
	return nil
}

func (h *APIHandler) document(w http.ResponseWriter, r *http.Request) error {
	caller, id, err := h.callerAndRequest(r)
	if err != nil {
		return err
	}
	doc, filename, err := h.svc.GetAPIDocument(r.Context(), caller.OrgID, id)
	if errors.Is(err, ErrNotCompleted) {
		return &respond.APIError{Status: http.StatusConflict, Code: "not_completed", Message: "the signed document is not ready yet"}
	}
	if err := mapStartError(err); err != nil {
		return err
	}
	writePDF(w, "attachment", signedName(filename), doc)
	return nil
}

type signerLinkResponse struct {
	SigningURL string `json:"signingUrl"`
}

func (h *APIHandler) signerLink(w http.ResponseWriter, r *http.Request) error {
	caller, id, err := h.callerAndRequest(r)
	if err != nil {
		return err
	}
	signerID, err := uuid.Parse(r.PathValue("signerID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid signer id"}
	}
	link, err := h.svc.IssueSignerLink(r.Context(), caller.OrgID, id, signerID)
	if errors.Is(err, ErrNotSigner) {
		// A member signs in the wallet and has no link; an unknown id has none either.
		return &respond.APIError{Status: http.StatusNotFound, Code: "signer_not_found", Message: "this request has no external signee with that id"}
	}
	if err := mapStartError(err); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	respond.JSON(w, r, http.StatusOK, signerLinkResponse{SigningURL: link})
	return nil
}

func (h *APIHandler) callerAndRequest(r *http.Request) (APICaller, uuid.UUID, error) {
	caller, err := h.auth.Caller(r.Context())
	if err != nil {
		return APICaller{}, uuid.Nil, err
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return APICaller{}, uuid.Nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	return caller, id, nil
}
