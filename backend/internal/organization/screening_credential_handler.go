package organization

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// startVogCredentialSession begins the opt-in pbdf.vog disclosure (#242 §4)
// for the membership a VOG link names - self-service only, like the PDF
// upload: the disclosure has to come from the member's own wallet.
func (h *Handler) startVogCredentialSession(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	sess, err := h.screening.StartVogCredentialSession(r.Context(), tc.OrganizationID)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

type completeVogCredentialRequest struct {
	DisclosureToken string `json:"disclosureToken"`
}

func decodeDisclosureToken(r *http.Request) (string, error) {
	var req completeVogCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", badRequest("invalid_body", "invalid request body")
	}
	if req.DisclosureToken == "" {
		return "", badRequest("invalid_input", "disclosureToken is required")
	}
	return req.DisclosureToken, nil
}

func (h *Handler) completeVogCredential(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	disclosureToken, err := decodeDisclosureToken(r)
	if err != nil {
		return err
	}

	outcome, err := h.screening.DiscloseVogCredential(r.Context(), tc.OrganizationID, tc.UserID, CheckedBySelf, nil, disclosureToken)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respondVogOutcome(w, r, outcome)
	return nil
}

// startIdentityVogCredentialSession / completeIdentityVogCredential are the
// combined identity + pbdf.vog disclosure for a member who has never
// identified (ScreeningService.DiscloseIdentityAndVogCredential).
func (h *Handler) startIdentityVogCredentialSession(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	sess, err := h.screening.StartIdentityVogCredentialSession(r.Context(), tc.OrganizationID)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

func (h *Handler) completeIdentityVogCredential(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	disclosureToken, err := decodeDisclosureToken(r)
	if err != nil {
		return err
	}

	outcome, err := h.screening.DiscloseIdentityAndVogCredential(r.Context(), tc.OrganizationID, tc.UserID, CheckedBySelf, nil, disclosureToken)
	if err := mapVogCredentialError(err); err != nil {
		return err
	}
	respondVogOutcome(w, r, outcome)
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
