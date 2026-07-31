package auth

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	// MeAvatarPath is the API path serving the signed-in user's own avatar. It is
	// also the path handed to the frontend in the /me response, so both sides agree
	// on one string.
	MeAvatarPath = "/api/v1/me/avatar"
	// avatarFormField is the multipart file field carrying the uploaded photo.
	avatarFormField = "avatar"
	// avatarMultipartMemory is how much of the form is buffered in RAM before
	// spilling to a temp file during parsing.
	avatarMultipartMemory = 1 << 20
	// avatarBodySlack allows for the multipart boundaries around the photo payload.
	avatarBodySlack = 1 << 16
)

// serveMyAvatar streams the signed-in user's own avatar.
func (h *Handler) serveMyAvatar(w http.ResponseWriter, r *http.Request) error {
	u := UserFromContext(r.Context())
	avatar, err := h.users.GetAvatar(r.Context(), u.ID)
	if errors.Is(err, user.ErrNoAvatar) || errors.Is(err, user.ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "no avatar set"}
	}
	if err != nil {
		return fmt.Errorf("getting own avatar: %w", err)
	}
	user.WriteAvatar(w, r, avatar)
	return nil
}

// putMyAvatar replaces the signed-in user's avatar with the uploaded photo. The
// upload is normalised (square, re-encoded, metadata dropped) before it is
// stored, so what lands in the database is never the raw camera file.
func (h *Handler) putMyAvatar(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, user.MaxAvatarUploadBytes+avatarBodySlack)
	if err := r.ParseMultipartForm(avatarMultipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return avatarTooLargeError()
		}
		return avatarBadRequest("invalid_body", "invalid multipart form")
	}

	file, _, err := r.FormFile(avatarFormField)
	if errors.Is(err, http.ErrMissingFile) {
		return avatarBadRequest("invalid_input", "no photo was uploaded")
	}
	if err != nil {
		return avatarBadRequest("invalid_body", "invalid photo upload")
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, user.MaxAvatarUploadBytes+1))
	if err != nil {
		return fmt.Errorf("reading uploaded avatar: %w", err)
	}
	switch {
	case len(data) == 0:
		return avatarBadRequest("invalid_input", "the photo file is empty")
	case len(data) > user.MaxAvatarUploadBytes:
		return avatarTooLargeError()
	}

	avatar, err := user.NormalizeAvatar(data)
	switch {
	case errors.Is(err, user.ErrAvatarUnsupported):
		return avatarBadRequest("invalid_input", "the photo must be a PNG, JPEG, GIF or WebP image")
	case errors.Is(err, user.ErrAvatarTooLarge):
		return avatarBadRequest("invalid_input", "the photo has too many pixels")
	case err != nil:
		return fmt.Errorf("normalising uploaded avatar: %w", err)
	}

	u := UserFromContext(r.Context())
	updated, err := h.users.SetAvatar(r.Context(), u.ID, avatar)
	if err != nil {
		return fmt.Errorf("storing avatar: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, h.meResponse(updated))
	return nil
}

// deleteMyAvatar removes the signed-in user's avatar, so they fall back to their
// initials again.
func (h *Handler) deleteMyAvatar(w http.ResponseWriter, r *http.Request) error {
	u := UserFromContext(r.Context())
	updated, err := h.users.ClearAvatar(r.Context(), u.ID)
	if err != nil {
		return fmt.Errorf("removing avatar: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, h.meResponse(updated))
	return nil
}

func avatarTooLargeError() error {
	return &respond.APIError{
		Status:  http.StatusRequestEntityTooLarge,
		Code:    "payload_too_large",
		Message: "the photo is too large",
	}
}

func avatarBadRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
