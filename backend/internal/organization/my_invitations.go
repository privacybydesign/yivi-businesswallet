package organization

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type myInvitationResponse struct {
	ID               uuid.UUID `json:"id"`
	OrganizationName string    `json:"organizationName"`
	OrganizationSlug string    `json:"organizationSlug"`
	GivenNames       string    `json:"givenNames"`
	LastName         string    `json:"lastName"`
	Email            string    `json:"email"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

func (h *Handler) myInvitations(w http.ResponseWriter, r *http.Request) error {
	email := auth.UserFromContext(r.Context()).Email
	invitations, err := h.service.MyInvitations(r.Context(), email)
	if err != nil {
		return fmt.Errorf("listing my invitations: %w", err)
	}
	out := make([]myInvitationResponse, 0, len(invitations))
	for _, inv := range invitations {
		out = append(out, myInvitationResponse{
			ID:               inv.ID,
			OrganizationName: inv.OrganizationName,
			OrganizationSlug: inv.OrganizationSlug,
			GivenNames:       inv.GivenNames,
			LastName:         inv.LastName,
			Email:            inv.Email,
			ExpiresAt:        inv.ExpiresAt,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) declineMyInvitation(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid invitation id")
	}
	email := auth.UserFromContext(r.Context()).Email
	if err := mapInviteError(h.service.DeclineInvitationForUser(r.Context(), id, email)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func mapAcceptError(err error) error {
	switch {
	case errors.Is(err, ErrDisclosureFailed):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "disclosure_failed", Message: "identity disclosure was not completed"}
	case errors.Is(err, ErrEmailMismatch):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "email_mismatch", Message: "the disclosed e-mail does not match the invitation"}
	case errors.Is(err, ErrNameMismatch):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "name_mismatch", Message: "the disclosed name does not match the invitation"}
	case errors.Is(err, ErrAlreadyMember):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_member", Message: "you are already a member of this organization"}
	default:
		return mapInviteError(err)
	}
}
