package organization

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type reidentifyPreviewResponse struct {
	OrganizationName string `json:"organizationName"`
	OrganizationSlug string `json:"organizationSlug"`
	Email            string `json:"email"`
}

// reidentifyPreview lets the re-identification page greet the member (org name,
// which address it is confirming) before starting a wallet session, the same
// role invitePreview plays for accept.
func (h *Handler) reidentifyPreview(w http.ResponseWriter, r *http.Request) error {
	rc, err := h.store.ReverifyTokenLookup(r.Context(), r.PathValue("token"))
	if err := mapReverifyError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, reidentifyPreviewResponse{
		OrganizationName: rc.OrganizationName,
		OrganizationSlug: rc.OrganizationSlug,
		Email:            rc.Email,
	})
	return nil
}

func (h *Handler) startReidentify(w http.ResponseWriter, r *http.Request) error {
	pkg, err := h.service.StartReverifySession(r.Context(), r.PathValue("token"))
	if err := mapReverifyError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, pkg)
	return nil
}

type completeReidentifyRequest struct {
	DisclosureToken string `json:"disclosureToken"`
}

type completeReidentifyResponse struct {
	OrganizationName string `json:"organizationName"`
	OrganizationSlug string `json:"organizationSlug"`
}

func (h *Handler) completeReidentify(w http.ResponseWriter, r *http.Request) error {
	var req completeReidentifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if req.DisclosureToken == "" {
		return badRequest("invalid_input", "disclosureToken is required")
	}

	outcome, err := h.service.CompleteReverification(r.Context(), r.PathValue("token"), req.DisclosureToken)
	if err := mapReverifyError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, completeReidentifyResponse(outcome))
	return nil
}

func mapReverifyError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrReverifyTokenNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "reidentify_link_not_found", Message: "this re-identification link is invalid or has expired"}
	case errors.Is(err, ErrDisclosureFailed):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "disclosure_failed", Message: "identity disclosure failed"}
	case errors.Is(err, ErrReverifyEmailMismatch):
		return &respond.APIError{Status: http.StatusConflict, Code: "email_mismatch", Message: "the disclosed e-mail does not match this member"}
	case errors.Is(err, ErrReverifyNameMismatch):
		return &respond.APIError{Status: http.StatusConflict, Code: "name_mismatch", Message: "the disclosed name does not match this member's identity on file; contact an organization admin"}
	case errors.Is(err, ErrCredentialTooOld):
		return &respond.APIError{Status: http.StatusConflict, Code: "credential_too_old", Message: "please refresh this credential in your wallet before re-identifying"}
	case errors.Is(err, ErrNotMember):
		return &respond.APIError{Status: http.StatusNotFound, Code: "member_not_found", Message: "member not found"}
	default:
		return err
	}
}
