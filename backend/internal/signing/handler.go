package signing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// maxUploadBytes bounds an uploaded PDF (10 MiB) so a large upload cannot exhaust
// memory; a QES demo signs ordinary documents.
const maxUploadBytes = 10 << 20

type signingService interface {
	StartLink(ctx context.Context, orgID, userID uuid.UUID, slug string) (Start, error)
	CreateRequest(ctx context.Context, orgID, createdBy uuid.UUID, slug, filename string, pdf []byte, signerIDs []uuid.UUID, mode string, rec RecipientInput) (uuid.UUID, error)
	StartSign(ctx context.Context, orgID, userID uuid.UUID, slug string, requestID uuid.UUID) (Start, error)
	HandleCallback(ctx context.Context, code, state string) string
	GetRequest(ctx context.Context, orgID, userID, id uuid.UUID, isAdmin bool) (Request, error)
	GetSignedDocument(ctx context.Context, orgID, userID, id uuid.UUID, isAdmin bool) ([]byte, string, error)
	ListPending(ctx context.Context, orgID, userID uuid.UUID) ([]Request, error)
	ListRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error)
	GetCredential(ctx context.Context, orgID, userID uuid.UUID) (LinkedCredential, error)
	Available(ctx context.Context, orgID uuid.UUID) (bool, error)
}

// Handler serves the co-signing workflow. The per-org routes are member-gated; the
// history list is additionally admin-gated. Routes that read a specific request
// widen access to org admins, else require the caller to be its creator or a
// signer. The OAuth callback is a central, unauthenticated route correlated by an
// unguessable state.
type Handler struct {
	svc         signingService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(svc signingService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{svc: svc, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/signing/availability", member(respond.HandlerFunc(h.getAvailability)))
	mux.Handle("GET /orgs/{slug}/signing/credential", member(respond.HandlerFunc(h.getCredential)))
	mux.Handle("POST /orgs/{slug}/signing/credential/link", member(respond.HandlerFunc(h.linkCredential)))
	mux.Handle("POST /orgs/{slug}/signing/requests", member(respond.HandlerFunc(h.createRequest)))
	mux.Handle("GET /orgs/{slug}/signing/requests", admin(respond.HandlerFunc(h.listRequests)))
	mux.Handle("GET /orgs/{slug}/signing/requests/pending", member(respond.HandlerFunc(h.listPending)))
	mux.Handle("POST /orgs/{slug}/signing/requests/{id}/sign", member(respond.HandlerFunc(h.signRequest)))
	mux.Handle("GET /orgs/{slug}/signing/requests/{id}", member(respond.HandlerFunc(h.getRequest)))
	mux.Handle("GET /orgs/{slug}/signing/requests/{id}/document", member(respond.HandlerFunc(h.getDocument)))

	// Central OAuth redirect target (matches the QTSP client registration). It is
	// correlated by state, so it needs no session; it 302s the browser onward.
	mux.HandleFunc("GET /signing/callback", h.callback)
}

// getAvailability reports whether a signing provider is configured for the org.
// Member-safe (no secret), so members — who cannot read the admin-only CSC
// settings — can still have the signing feature gated for them in the UI.
func (h *Handler) getAvailability(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	available, err := h.svc.Available(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("checking signing availability: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, map[string]bool{"available": available})
	return nil
}

func (h *Handler) getCredential(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	cred, err := h.svc.GetCredential(r.Context(), org.ID, u.ID)
	if errors.Is(err, ErrNoCredential) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "no_credential", Message: "no signing credential linked yet"}
	}
	if err != nil {
		return fmt.Errorf("getting linked credential: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, cred)
	return nil
}

func (h *Handler) linkCredential(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	start, err := h.svc.StartLink(r.Context(), org.ID, u.ID, org.Slug)
	if err := h.mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, start)
	return nil
}

// createRequest stores a new co-signing request: a PDF plus the selected signers,
// mode and recipient. It does not sign — each signer signs later via signRequest.
func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_upload", Message: "could not read the uploaded document"}
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "missing_document", Message: "a PDF document is required in the 'document' field"}
	}
	defer func() { _ = file.Close() }()
	pdf, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_upload", Message: "could not read the uploaded document"}
	}
	if len(pdf) > maxUploadBytes {
		return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "document_too_large", Message: "the document is too large"}
	}

	signerIDs, err := parseSignerIDs(r.Form["signerIds"])
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_signers", Message: "select one or more valid signers"}
	}
	rec := RecipientInput{
		Channel: strings.TrimSpace(r.FormValue("recipientChannel")),
		Address: strings.TrimSpace(r.FormValue("recipientAddress")),
		Name:    strings.TrimSpace(r.FormValue("recipientName")),
		Message: r.FormValue("message"),
	}
	if rec.Channel == "" {
		rec.Channel = ChannelNone
	}
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode == "" {
		mode = ModeParallel
	}

	id, err := h.svc.CreateRequest(r.Context(), org.ID, u.ID, org.Slug, header.Filename, pdf, signerIDs, mode, rec)
	if err := h.mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusCreated, map[string]string{"id": id.String()})
	return nil
}

// signRequest starts the acting user's signing ceremony for a request and returns
// the authorize URL to hand off to.
func (h *Handler) signRequest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	start, err := h.svc.StartSign(r.Context(), org.ID, u.ID, org.Slug, id)
	if err := h.mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, start)
	return nil
}

func (h *Handler) listPending(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	reqs, err := h.svc.ListPending(r.Context(), org.ID, u.ID)
	if err != nil {
		return fmt.Errorf("listing pending signing requests: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, map[string]any{"requests": reqs})
	return nil
}

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	// A missing or unparseable limit becomes 0; Store.ListRequests is the single
	// authority that clamps it into range (see its page-limit consts).
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	reqs, next, err := h.svc.ListRequests(r.Context(), org.ID, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, ErrInvalidRequest) {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "invalid pagination cursor"}
	}
	if err != nil {
		return fmt.Errorf("listing signing requests: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, map[string]any{"requests": reqs, "nextCursor": next})
	return nil
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	req, err := h.svc.GetRequest(r.Context(), org.ID, u.ID, id, organization.IsAdmin(r.Context()))
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "signing request not found"}
	}
	if err != nil {
		return fmt.Errorf("getting signing request: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, req)
	return nil
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	doc, filename, err := h.svc.GetSignedDocument(r.Context(), org.ID, u.ID, id, organization.IsAdmin(r.Context()))
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "signing request not found"}
	}
	if errors.Is(err, ErrNotCompleted) {
		return &respond.APIError{Status: http.StatusConflict, Code: "not_completed", Message: "the signed document is not ready yet"}
	}
	if err != nil {
		return fmt.Errorf("getting signed document: %w", err)
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", signedName(filename)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
	return nil
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	dest := h.svc.HandleCallback(r.Context(), code, state)
	http.Redirect(w, r, dest, http.StatusFound)
}

// parseSignerIDs parses the repeated signerIds form values into unique user ids.
// It requires at least one and rejects any malformed value.
func parseSignerIDs(values []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, errors.New("signing: no signers selected")
	}
	return out, nil
}

// mapStartError translates domain errors into API errors.
func (h *Handler) mapStartError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotConfigured):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_configured", Message: "configure a CSC signing provider in settings first"}
	case errors.Is(err, ErrNoCredential):
		return &respond.APIError{Status: http.StatusConflict, Code: "no_credential", Message: "link a signing credential before signing"}
	case errors.Is(err, ErrInvalidPDF):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_pdf", Message: "the uploaded file is not a valid PDF"}
	case errors.Is(err, ErrInvalidRequest):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "the signing request is invalid"}
	case errors.Is(err, ErrNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "signing request not found"}
	case errors.Is(err, ErrNotSigner):
		return &respond.APIError{Status: http.StatusForbidden, Code: "not_signer", Message: "you are not a signer of this request"}
	case errors.Is(err, ErrAlreadySigned):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_signed", Message: "you have already signed this document"}
	case errors.Is(err, ErrNotYourTurn):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_your_turn", Message: "an earlier signer must sign first"}
	case errors.Is(err, ErrSignInProgress):
		return &respond.APIError{Status: http.StatusConflict, Code: "sign_in_progress", Message: "another signature is in progress; try again shortly"}
	default:
		return fmt.Errorf("starting signing ceremony: %w", err)
	}
}

func signedName(filename string) string {
	if filename == "" {
		return "signed.pdf"
	}
	return "signed-" + filename
}
