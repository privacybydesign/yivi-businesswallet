package organization

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// startVogCredentialSession begins the opt-in pbdf.vog disclosure (#242 §4) for
// the caller's own membership - self-service only, like the PDF upload.
func (h *Handler) startVogCredentialSession(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	sess, err := h.screening.StartVogCredentialSession(r.Context(), org.ID)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

type completeVogCredentialRequest struct {
	DisclosureToken string `json:"disclosureToken"`
}

func (h *Handler) completeVogCredential(w http.ResponseWriter, r *http.Request) error {
	var req completeVogCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if req.DisclosureToken == "" {
		return badRequest("invalid_input", "disclosureToken is required")
	}

	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	outcome, err := h.screening.DiscloseVogCredential(r.Context(), org.ID, actor.ID, CheckedBySelf, nil, req.DisclosureToken)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}

	resp := uploadVogResponse{Result: string(outcome.Result), MissingCodes: outcome.MissingCodes, RejectionReason: outcome.RejectionReason}
	respond.JSON(w, r, vogUploadStatus(outcome.RejectionReason), resp)
	return nil
}

// startIdentityVogCredentialSession / completeIdentityVogCredential are the
// combined identity + pbdf.vog disclosure for a member who has never
// identified (ScreeningService.DiscloseIdentityAndVogCredential).
func (h *Handler) startIdentityVogCredentialSession(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	sess, err := h.screening.StartIdentityVogCredentialSession(r.Context(), org.ID)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

func (h *Handler) completeIdentityVogCredential(w http.ResponseWriter, r *http.Request) error {
	var req completeVogCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if req.DisclosureToken == "" {
		return badRequest("invalid_input", "disclosureToken is required")
	}

	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	outcome, err := h.screening.DiscloseIdentityAndVogCredential(r.Context(), org.ID, actor.ID, CheckedBySelf, nil, req.DisclosureToken)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}

	resp := uploadVogResponse{Result: string(outcome.Result), MissingCodes: outcome.MissingCodes, RejectionReason: outcome.RejectionReason}
	respond.JSON(w, r, vogUploadStatus(outcome.RejectionReason), resp)
	return nil
}

func mapVogCredentialError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrVogCredentialNotAccepted):
		return &respond.APIError{Status: http.StatusConflict, Code: "vog_credential_not_accepted", Message: "this organization does not accept the pbdf.vog credential"}
	case errors.Is(err, ErrDisclosureFailed), errors.Is(err, ErrReverifyEmailMismatch), errors.Is(err, ErrReverifyNameMismatch), errors.Is(err, ErrCredentialTooOld):
		// The identity half of a combined disclosure fails the way a
		// re-identification does, with the same codes the frontend already knows.
		return mapReverifyError(err)
	default:
		return mapScreeningError(err)
	}
}
