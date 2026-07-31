package useravatar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	// multipartMemory is how much of the form is buffered in RAM before spilling
	// to temp files during parsing.
	multipartMemory = 1 << 20
	// bodySlack allows for the multipart boundaries on top of the photo cap.
	bodySlack = 1 << 20
	// photoFormField is the multipart file field carrying the photo.
	photoFormField = "photo"
)

type avatarStore interface {
	Get(ctx context.Context, userID uuid.UUID) (Avatar, error)
	GetForOrgMember(ctx context.Context, userID, orgID uuid.UUID) (Avatar, error)
	Save(ctx context.Context, userID uuid.UUID, a Avatar) (time.Time, error)
	Delete(ctx context.Context, userID uuid.UUID) error
}

// Handler serves the avatar routes: a user manages their own photo under /me, and
// an organisation administrator reads a member's photo under that organisation.
type Handler struct {
	store       avatarStore
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store avatarStore, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	// A member's photo is readable by the organisation's administrators, matching
	// the routes that render it (the member list, a member's page and the audit
	// log are all admin-only). Data minimisation: nobody else in the org needs it.
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /me/avatar", h.requireUser(respond.HandlerFunc(h.serveMine)))
	mux.Handle("PUT /me/avatar", h.requireUser(respond.HandlerFunc(h.putMine)))
	mux.Handle("DELETE /me/avatar", h.requireUser(respond.HandlerFunc(h.deleteMine)))
	mux.Handle("GET /orgs/{slug}/members/{userId}/avatar", admin(respond.HandlerFunc(h.serveMember)))
}

// avatarResponse is what a change to the photo returns: the versioned path the
// new photo is served from, or "" once it has been removed.
type avatarResponse struct {
	AvatarURI string `json:"avatarUri"`
}

func (h *Handler) serveMine(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	avatar, err := h.store.Get(r.Context(), u.ID)
	if errors.Is(err, ErrNoAvatar) {
		return apiError(http.StatusNotFound, "not_found", "no avatar set")
	}
	if err != nil {
		return fmt.Errorf("getting own avatar: %w", err)
	}
	writeAvatar(w, r, avatar)
	return nil
}

func (h *Handler) serveMember(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_user_id", "user id must be a UUID")
	}
	org := organization.OrgFromContext(r.Context())
	avatar, err := h.store.GetForOrgMember(r.Context(), userID, org.ID)
	if errors.Is(err, ErrNoAvatar) {
		return apiError(http.StatusNotFound, "not_found", "no avatar set")
	}
	if err != nil {
		return fmt.Errorf("getting member avatar: %w", err)
	}
	writeAvatar(w, r, avatar)
	return nil
}

// putMine replaces the caller's own photo with the uploaded one.
func (h *Handler) putMine(w http.ResponseWriter, r *http.Request) error {
	avatar, err := parseUpload(w, r)
	if err != nil {
		return err
	}

	u := auth.UserFromContext(r.Context())
	updatedAt, err := h.store.Save(r.Context(), u.ID, avatar)
	if err != nil {
		return fmt.Errorf("saving own avatar: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, avatarResponse{AvatarURI: user.AvatarURL(&updatedAt)})
	return nil
}

func (h *Handler) deleteMine(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	if err := h.store.Delete(r.Context(), u.ID); err != nil {
		return fmt.Errorf("deleting own avatar: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, avatarResponse{})
	return nil
}

// parseUpload reads the "photo" file part and normalizes it for storage.
func parseUpload(w http.ResponseWriter, r *http.Request) (Avatar, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxAvatarBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return Avatar{}, apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the photo is too large")
		}
		return Avatar{}, badRequest("invalid_body", "invalid multipart form")
	}

	file, _, err := r.FormFile(photoFormField)
	if errors.Is(err, http.ErrMissingFile) {
		return Avatar{}, badRequest("invalid_input", "no photo was uploaded")
	}
	if err != nil {
		return Avatar{}, badRequest("invalid_body", "invalid photo upload")
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, MaxAvatarBytes+1))
	if err != nil {
		return Avatar{}, fmt.Errorf("reading uploaded photo: %w", err)
	}
	if len(data) == 0 {
		return Avatar{}, badRequest("invalid_input", "the photo file is empty")
	}
	if len(data) > MaxAvatarBytes {
		return Avatar{}, apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the photo is too large")
	}
	return normalize(data)
}

// writeAvatar streams the stored image bytes with a locked-down response (see
// setAvatarResponseHeaders).
func writeAvatar(w http.ResponseWriter, r *http.Request, avatar Avatar) {
	setAvatarResponseHeaders(w.Header(), avatar.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(avatar.Bytes); err != nil {
		// The status and headers are already committed, so an error here can only
		// be logged, not turned into an API error response.
		slog.ErrorContext(r.Context(), "useravatar: write avatar body", slog.String("error", err.Error()))
	}
}

// setAvatarResponseHeaders locks the avatar response down. The bytes are
// user-uploaded content served same-origin, so nosniff keeps the declared type
// authoritative and the null-source CSP plus sandbox stop anything the browser
// might still be talked into treating as a document. The photo is personal data,
// so the cache is private.
func setAvatarResponseHeaders(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=300")
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}

func badRequest(code, msg string) error {
	return apiError(http.StatusBadRequest, code, msg)
}
