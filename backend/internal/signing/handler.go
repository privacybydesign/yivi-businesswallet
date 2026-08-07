package signing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

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
	StartSign(ctx context.Context, orgID, userID uuid.UUID, slug, filename string, pdf []byte) (Start, error)
	HandleCallback(ctx context.Context, code, state string) string
	GetRequest(ctx context.Context, orgID, userID, id uuid.UUID) (Request, error)
	GetSignedDocument(ctx context.Context, orgID, userID, id uuid.UUID) ([]byte, string, error)
	GetCredential(ctx context.Context, orgID, userID uuid.UUID) (LinkedCredential, error)
}

// Handler serves the signing ceremony. The per-org routes are member-gated and
// act on the calling user's own credential/requests; the OAuth callback is a
// central, unauthenticated route correlated by an unguessable state.
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
	mux.Handle("GET /orgs/{slug}/signing/credential", member(respond.HandlerFunc(h.getCredential)))
	mux.Handle("POST /orgs/{slug}/signing/credential/link", member(respond.HandlerFunc(h.linkCredential)))
	mux.Handle("POST /orgs/{slug}/signing/requests", member(respond.HandlerFunc(h.createRequest)))
	mux.Handle("GET /orgs/{slug}/signing/requests/{id}", member(respond.HandlerFunc(h.getRequest)))
	mux.Handle("GET /orgs/{slug}/signing/requests/{id}/document", member(respond.HandlerFunc(h.getDocument)))

	// Central OAuth redirect target (matches the QTSP client registration). It is
	// correlated by state, so it needs no session; it 302s the browser onward.
	mux.HandleFunc("GET /signing/callback", h.callback)
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

	start, err := h.svc.StartSign(r.Context(), org.ID, u.ID, org.Slug, header.Filename, pdf)
	if err := h.mapStartError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, start)
	return nil
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	req, err := h.svc.GetRequest(r.Context(), org.ID, u.ID, id)
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
	doc, filename, err := h.svc.GetSignedDocument(r.Context(), org.ID, u.ID, id)
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

// mapStartError translates domain errors from StartLink/StartSign into API errors.
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
