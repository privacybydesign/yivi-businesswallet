package organization

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func (h *Handler) listIdentityReviews(w http.ResponseWriter, r *http.Request) error {
	reviews, err := h.service.ListIdentityReviews(r.Context())
	if err != nil {
		return fmt.Errorf("listing identity reviews: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, reviews)
	return nil
}

func (h *Handler) approveIdentityReview(w http.ResponseWriter, r *http.Request) error {
	return h.resolveIdentityReview(w, r, true)
}

func (h *Handler) rejectIdentityReview(w http.ResponseWriter, r *http.Request) error {
	return h.resolveIdentityReview(w, r, false)
}

func (h *Handler) resolveIdentityReview(w http.ResponseWriter, r *http.Request, approve bool) error {
	reviewID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid review id")
	}
	reviewer := auth.UserFromContext(r.Context()).ID

	outcome, err := h.service.ResolveIdentityReview(r.Context(), reviewID, reviewer, approve)
	switch {
	case errors.Is(err, ErrReviewNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "review_not_found", Message: "identity review not found"}
	case errors.Is(err, ErrReviewResolved):
		return &respond.APIError{Status: http.StatusConflict, Code: "review_resolved", Message: "this review has already been resolved"}
	case errors.Is(err, ErrAlreadyMember):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_member", Message: "the user is already a member of this organization"}
	case err != nil:
		return fmt.Errorf("resolving identity review: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, resolveReviewResponse{
		Approved:         outcome.Approved,
		OrganizationName: outcome.OrganizationName,
		OrganizationSlug: outcome.OrganizationSlug,
	})
	return nil
}

type resolveReviewResponse struct {
	Approved         bool   `json:"approved"`
	OrganizationName string `json:"organizationName"`
	OrganizationSlug string `json:"organizationSlug"`
}
