package postguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	// maxUploadBytes bounds a single send request body; keep in step with the
	// sidecar's PG_MAX_UPLOAD_BYTES (default 100 MB).
	maxUploadBytes = 100 << 20
	// multipartMemory is how much of the form is buffered in RAM before spilling
	// to temp files during parsing.
	multipartMemory = 32 << 20
	// bodySlack allows for multipart boundaries and field values on top of the
	// file payload cap.
	bodySlack = 1 << 20
)

// postguardService is the surface the handler depends on.
type postguardService interface {
	APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error)
	SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error
	DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error
	ListSentFiles(ctx context.Context, orgID uuid.UUID) ([]SentFile, error)
	Send(ctx context.Context, orgID, senderUserID uuid.UUID, in SendInput) (SentFile, error)
}

// Handler serves the org-scoped PostGuard routes. Key management is admin-only;
// listing and sending files is available to any member.
type Handler struct {
	service     postguardService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(service postguardService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	orgAdmin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}

	mux.Handle("GET /orgs/{slug}/postguard/settings", orgScoped(respond.HandlerFunc(h.settings)))
	mux.Handle("PUT /orgs/{slug}/postguard/api-key", orgAdmin(respond.HandlerFunc(h.setAPIKey)))
	mux.Handle("DELETE /orgs/{slug}/postguard/api-key", orgAdmin(respond.HandlerFunc(h.deleteAPIKey)))
	mux.Handle("GET /orgs/{slug}/postguard/files", orgScoped(respond.HandlerFunc(h.listFiles)))
	mux.Handle("POST /orgs/{slug}/postguard/files", orgScoped(respond.HandlerFunc(h.send)))
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	info, err := h.service.APIKeyInfo(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusOK, info)
	return nil
}

type setAPIKeyRequest struct {
	APIKey string `json:"apiKey"`
}

func (h *Handler) setAPIKey(w http.ResponseWriter, r *http.Request) error {
	var req setAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	org := organization.OrgFromContext(r.Context())
	if e := mapError(h.service.SetAPIKey(r.Context(), org.ID, req.APIKey)); e != nil {
		return e
	}
	info, err := h.service.APIKeyInfo(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusOK, info)
	return nil
}

func (h *Handler) deleteAPIKey(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	if e := mapError(h.service.DeleteAPIKey(r.Context(), org.ID)); e != nil {
		return e
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	files, err := h.service.ListSentFiles(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusOK, files)
	return nil
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the upload is too large")
		}
		return badRequest("invalid_body", "invalid multipart form")
	}

	input, err := parseSendForm(r)
	if err != nil {
		return mapError(err)
	}

	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	sent, err := h.service.Send(r.Context(), org.ID, u.ID, input)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusCreated, sent)
	return nil
}

func parseSendForm(r *http.Request) (SendInput, error) {
	recipients := make([]string, 0)
	for _, raw := range r.Form["recipients"] {
		if v := strings.TrimSpace(raw); v != "" {
			recipients = append(recipients, v)
		}
	}
	if len(recipients) == 0 {
		return SendInput{}, ErrNoRecipients
	}

	var files []FileBlob
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["file"] {
			f, err := fh.Open()
			if err != nil {
				return SendInput{}, fmt.Errorf("postguard: open uploaded file: %w", err)
			}
			data, err := readAllAndClose(f)
			if err != nil {
				return SendInput{}, fmt.Errorf("postguard: read uploaded file: %w", err)
			}
			files = append(files, FileBlob{
				Name:        fh.Filename,
				ContentType: fh.Header.Get("Content-Type"),
				Data:        data,
			})
		}
	}
	if len(files) == 0 {
		return SendInput{}, ErrNoFiles
	}

	return SendInput{
		Recipients:   recipients,
		Files:        files,
		Notify:       r.FormValue("notify") != "false",
		Message:      strings.TrimSpace(r.FormValue("message")),
		ExpiresAfter: strings.TrimSpace(r.FormValue("expiresAfter")),
	}, nil
}

func readAllAndClose(f multipart.File) ([]byte, error) {
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}

// mapError translates postguard errors to API errors.
func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotConfigured):
		return apiError(http.StatusServiceUnavailable, "postguard_not_configured", "PostGuard is not configured on this deployment")
	case errors.Is(err, ErrKeyNotSet):
		return apiError(http.StatusConflict, "api_key_not_set", "no PostGuard API key is configured for this organization")
	case errors.Is(err, ErrInvalidAPIKey):
		return badRequest("invalid_api_key", "the API key is not a valid PostGuard for Business key")
	case errors.Is(err, ErrNoRecipients):
		return badRequest("no_recipients", "at least one recipient is required")
	case errors.Is(err, ErrNoFiles):
		return badRequest("no_files", "at least one file is required")
	case errors.Is(err, ErrPayloadTooLarge):
		return apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the upload is too large")
	case errors.Is(err, ErrSidecar):
		return apiError(http.StatusBadGateway, "postguard_upstream", "PostGuard could not process the request")
	default:
		return fmt.Errorf("postguard: %w", err)
	}
}
