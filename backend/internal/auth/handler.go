package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// avatarStore is the slice of the user store the /me/avatar routes need: the
// signed-in user's own portrait photo, read and written on their own behalf.
type avatarStore interface {
	GetAvatar(ctx context.Context, id uuid.UUID) (user.Avatar, error)
	SetAvatar(ctx context.Context, id uuid.UUID, a user.Avatar) (user.User, error)
	ClearAvatar(ctx context.Context, id uuid.UUID) (user.User, error)
}

type Handler struct {
	svc      *Service
	sessions sessionLookuper
	users    avatarStore
	cookie   CookieConfig
	admins   PlatformAdmins
}

func NewHandler(svc *Service, sessions sessionLookuper, users avatarStore, cookie CookieConfig, admins PlatformAdmins) *Handler {
	return &Handler{
		svc:      svc,
		sessions: sessions,
		users:    users,
		cookie:   cookie,
		admins:   admins,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /auth/session", respond.HandlerFunc(h.startSession))
	mux.Handle("GET /auth/session/{id}/status", respond.HandlerFunc(h.status))
	mux.Handle("POST /auth/session/{id}/claim", respond.HandlerFunc(h.claim))
	mux.Handle("POST /auth/logout", respond.HandlerFunc(h.logout))

	authed := RequireUser(h.sessions)
	mux.Handle("GET /me", authed(respond.HandlerFunc(h.me)))
	mux.Handle("GET /me/avatar", authed(respond.HandlerFunc(h.serveMyAvatar)))
	mux.Handle("PUT /me/avatar", authed(respond.HandlerFunc(h.putMyAvatar)))
	mux.Handle("DELETE /me/avatar", authed(respond.HandlerFunc(h.deleteMyAvatar)))
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) error {
	sess, err := h.svc.StartSession(r.Context())
	if err != nil {
		return err
	}

	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	st, err := h.svc.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		return fmt.Errorf("auth: session status: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, statusResponse{Status: st})
	return nil
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) error {
	u, raw, err := h.svc.Authenticate(r.Context(), r.PathValue("id"))
	if err != nil {
		var invited *PendingInvitesError
		if errors.As(err, &invited) {
			respond.JSON(w, r, http.StatusOK, toPendingInvitationsResponse(invited.Invites))
			return nil
		}
		return mapAuthError(err)
	}
	setSessionCookie(w, raw, h.cookie)

	respond.JSON(w, r, http.StatusOK, h.meResponse(u))
	return nil
}

type pendingInvitationsResponse struct {
	PendingInvitations []pendingInvitation `json:"pendingInvitations"`
}

type pendingInvitation struct {
	ID               uuid.UUID `json:"id"`
	OrganizationName string    `json:"organizationName"`
	OrganizationSlug string    `json:"organizationSlug"`
}

func toPendingInvitationsResponse(invites []PendingInvite) pendingInvitationsResponse {
	out := pendingInvitationsResponse{PendingInvitations: make([]pendingInvitation, 0, len(invites))}
	for _, inv := range invites {
		out.PendingInvitations = append(out.PendingInvitations, pendingInvitation(inv))
	}
	return out
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	u := UserFromContext(r.Context())
	respond.JSON(w, r, http.StatusOK, h.meResponse(u))
	return nil
}

func (h *Handler) meResponse(u user.User) meResponse {
	return meResponse{
		ID:              u.ID,
		Email:           string(u.Email),
		PreferredName:   u.PreferredName,
		GivenNames:      u.GivenNames,
		LastName:        u.LastName,
		AvatarURI:       user.AvatarURL(MeAvatarPath, u.HasAvatar, u.AvatarUpdatedAt),
		IsPlatformAdmin: h.admins.Has(u.Email),
	}
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	if raw, ok := readSessionCookie(r); ok {
		if err := h.svc.Logout(r.Context(), raw); err != nil {
			return err
		}
	}
	clearSessionCookie(w, h.cookie)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, errSessionNotFinished), errors.Is(err, errDisclosureInvalid), errors.Is(err, errUserNotInvited):
		return mapClaimError(err)
	default:
		return err
	}
}

type statusResponse struct {
	Status string `json:"status"`
}

type meResponse struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	PreferredName *string   `json:"preferredName"`
	GivenNames    string    `json:"givenNames"`
	LastName      string    `json:"lastName"`
	// AvatarURI is the API path serving the user's own portrait photo, "" when they
	// have not set one (the frontend then shows their initials).
	AvatarURI       string `json:"avatarUri"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
}
