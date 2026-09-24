package organization

import (
	"fmt"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The member's VOG submission is a public page keyed by a bearer token
// (/vog/<token>), the same shape as a credential claim or a re-identification
// link: the member opens the e-mail on whatever device they have and submits
// without signing in. The token is what identifies the membership. It is not
// a bearer key to the account, and a leaked link cannot plant a VOG on the
// member: every path still has to match the member's stored name and date of
// birth, against a Justis-validated PDF or a credential from a wallet.

// vogURL is the public submission link for a raw token.
func (h *Handler) vogURL(rawToken string) string {
	return h.appBaseURL + "/vog/" + rawToken
}

// vogTokenContext resolves the request's {token} path value to its membership.
func (h *Handler) vogTokenContext(r *http.Request) (VogTokenContext, error) {
	tc, err := h.store.VogTokenLookup(r.Context(), r.PathValue("token"))
	if err := mapScreeningError(err); err != nil {
		return VogTokenContext{}, err
	}
	return tc, nil
}

type vogPreviewResponse struct {
	OrganizationName string `json:"organizationName"`
	OrganizationSlug string `json:"organizationSlug"`
	Email            string `json:"email"`
	ownVogState
}

// vogPreview lets the VOG page greet the member and pick which options to
// show - identify first, PDF upload, the wallet credential - before anything
// is submitted, the role reidentifyPreview plays for re-identification.
func (h *Handler) vogPreview(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	state, err := h.vogState(r.Context(), tc.OrganizationID, tc.UserID)
	if err := mapScreeningError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, vogPreviewResponse{
		OrganizationName: tc.OrganizationName,
		OrganizationSlug: tc.OrganizationSlug,
		Email:            tc.Email,
		ownVogState:      state,
	})
	return nil
}

// startVogIdentitySession / completeVogIdentification let a member who has
// never identified (no date of birth on file) identify from the VOG page, so
// a VOG has something to be matched against (Service.CompleteOwnIdentification).
func (h *Handler) startVogIdentitySession(w http.ResponseWriter, r *http.Request) error {
	if _, err := h.vogTokenContext(r); err != nil {
		return err
	}
	sess, err := h.service.StartIdentitySession(r.Context())
	if err != nil {
		return fmt.Errorf("starting vog identity session: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

func (h *Handler) completeVogIdentification(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	disclosureToken, err := decodeDisclosureToken(r)
	if err != nil {
		return err
	}
	if err := mapReverifyError(h.service.CompleteOwnIdentification(r.Context(), tc.OrganizationID, tc.UserID, disclosureToken)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type mintVogTokenResponse struct {
	VogURL string `json:"vogUrl"`
}

// mintOwnVogToken backs the dashboard banner: a member asked for a VOG mints
// their own link rather than waiting on e-mail, through the exact mechanism a
// reminder or an admin request uses.
func (h *Handler) mintOwnVogToken(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	token, _, err := h.store.EnsureVogToken(r.Context(), org.ID, actor.ID)
	if err := mapScreeningError(err); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, mintVogTokenResponse{VogURL: h.vogURL(token)})
	return nil
}
