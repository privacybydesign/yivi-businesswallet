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
	switch {
	case errors.Is(err, ErrDisclosureFailed):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "disclosure_failed", Message: "identity disclosure was not completed"}
	case errors.Is(err, ErrEmailMismatch):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "email_mismatch", Message: "the disclosed e-mail does not match the invitation"}
	case errors.Is(err, ErrNameMismatch):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "name_mismatch", Message: "the disclosed name does not match the invitation"}
	case errors.Is(err, ErrIdentityReview):
		return &respond.APIError{Status: http.StatusConflict, Code: "identity_review_required", Message: "this identity needs review before you can join"}
	case errors.Is(err, ErrAlreadyMember):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_member", Message: "you are already a member of this organization"}
	}
	if err := mapInviteError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, acceptResponse{
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
