package organization

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type invitePreviewResponse struct {
	OrganizationName string    `json:"organizationName"`
	OrganizationSlug string    `json:"organizationSlug"`
	GivenNames       string    `json:"givenNames"`
	LastName         string    `json:"lastName"`
	Email            string    `json:"email"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type acceptRequest struct {
	DisclosureToken string `json:"disclosureToken"`
}

type acceptResponse struct {
	Status           string `json:"status"`
	OrganizationName string `json:"organizationName"`
	OrganizationSlug string `json:"organizationSlug"`
}

func (h *Handler) invitePreview(w http.ResponseWriter, r *http.Request) error {
	inv, err := h.service.PendingInvitation(r.Context(), r.PathValue("token"))
	if err := mapInviteError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, invitePreviewResponse{
		OrganizationName: inv.OrganizationName,
		OrganizationSlug: inv.OrganizationSlug,
		GivenNames:       inv.GivenNames,
		LastName:         inv.LastName,
		Email:            inv.Email,
		ExpiresAt:        inv.ExpiresAt,
	})
	return nil
}

func (h *Handler) startAccept(w http.ResponseWriter, r *http.Request) error {
	pkg, err := h.service.StartAcceptSession(r.Context(), r.PathValue("token"))
	if err := mapInviteError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, pkg)
	return nil
}

func (h *Handler) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	var req acceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if req.DisclosureToken == "" {
		return badRequest("invalid_input", "disclosureToken is required")
	}

	outcome, err := h.service.AcceptInvitation(r.Context(), r.PathValue("token"), req.DisclosureToken)
	if err := mapAcceptError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, acceptResponse{
		Status:           string(outcome.Status),
		OrganizationName: outcome.OrganizationName,
		OrganizationSlug: outcome.OrganizationSlug,
	})
	return nil
}

func (h *Handler) declineInvite(w http.ResponseWriter, r *http.Request) error {
	if err := mapInviteError(h.service.DeclineInvitation(r.Context(), r.PathValue("token"))); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func mapInviteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvitationNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "invitation_not_found", Message: "invitation not found"}
	case errors.Is(err, ErrInvitationExpired):
		return &respond.APIError{Status: http.StatusGone, Code: "invitation_expired", Message: "this invitation has expired"}
	default:
		return err
	}
}
