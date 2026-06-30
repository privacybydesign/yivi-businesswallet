package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	irma "github.com/privacybydesign/irmago/irma"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/irmarequestor"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type Handler struct {
	svc      *Service
	sessions sessionLookuper
	cookie   CookieConfig
	admins   PlatformAdmins
}

func NewHandler(svc *Service, sessions sessionLookuper, cookie CookieConfig, admins PlatformAdmins) *Handler {
	return &Handler{
		svc:      svc,
		sessions: sessions,
		cookie:   cookie,
		admins:   admins,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /auth/session", respond.HandlerFunc(h.startSession))
	mux.Handle("GET /auth/session/{token}/status", respond.HandlerFunc(h.status))
	mux.Handle("POST /auth/session/{token}/claim", respond.HandlerFunc(h.claim))
	mux.Handle("POST /auth/logout", respond.HandlerFunc(h.logout))

	authed := RequireUser(h.sessions)
	mux.Handle("GET /me", authed(respond.HandlerFunc(h.me)))
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) error {
	pkg, err := h.svc.StartSession(r.Context())
	if err != nil {
		return err
	}

	respond.JSON(w, r, http.StatusOK, pkg)
	return nil
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	token := irma.RequestorToken(r.PathValue("token"))

	st, err := h.svc.Status(r.Context(), token)
	if err != nil {
		return unknownSessionOr(err)
	}

	respond.JSON(w, r, http.StatusOK, statusResponse{Status: string(st)})
	return nil
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) error {
	token := irma.RequestorToken(r.PathValue("token"))

	u, raw, err := h.svc.Authenticate(r.Context(), token)
	if err != nil {
		return mapAuthError(err)
	}
	setSessionCookie(w, raw, h.cookie)

	respond.JSON(w, r, http.StatusOK, h.meResponse(u))
	return nil
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

func unknownSessionOr(err error) error {
	if errors.Is(err, irmarequestor.ErrUnknownSession) {
		return &respond.APIError{
			Status:  http.StatusNotFound,
			Code:    "unknown_session",
			Message: "session not found",
		}
	}
	return fmt.Errorf("auth: session lookup: %w", err)
}

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, irmarequestor.ErrUnknownSession):
		return unknownSessionOr(err)
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
	ID              uuid.UUID `json:"id"`
	Email           string    `json:"email"`
	PreferredName   *string   `json:"preferredName"`
	GivenNames      string    `json:"givenNames"`
	LastName        string    `json:"lastName"`
	IsPlatformAdmin bool      `json:"isPlatformAdmin"`
}
