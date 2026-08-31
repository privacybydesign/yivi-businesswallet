package organization

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// maxRevocationReasonLength bounds the free-text reason so a revocation cannot
// push an unbounded string into the audit log.
const maxRevocationReasonLength = 500

func (h *Handler) listMandates(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	mandates, err := h.store.ListMandates(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing mandates: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, mandates)
	return nil
}

// mandateAuthorityResponse is the caller's own basis of authority in this
// organization. The register screen needs it to offer the grant and revoke
// actions to exactly the callers RequireMandateAuthority would let through, and
// to say why it is withholding them from anyone else.
type mandateAuthorityResponse struct {
	MayGrant            bool `json:"mayGrant"`
	LegalRepresentative bool `json:"legalRepresentative"`
	FullMandate         bool `json:"fullMandate"`
	// JointAuthority marks a `jointly` registered director. This layer honours
	// them as a sole representative, which is the one gap that would let the UI
	// imply a capability the backend cannot yet get right.
	JointAuthority bool `json:"jointAuthority"`
}

func (h *Handler) mandateAuthority(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	caller := auth.UserFromContext(r.Context())
	joint, err := h.store.HasJointRepresentation(r.Context(), org.ID, caller.ID)
	if err != nil {
		return fmt.Errorf("resolving joint representation: %w", err)
	}

	authority := AuthorityFromContext(r.Context())
	respond.JSON(w, r, http.StatusOK, mandateAuthorityResponse{
		MayGrant:            authority.MayGrantMandate(),
		LegalRepresentative: authority.LegalRepresentative,
		FullMandate:         authority.FullMandate,
		JointAuthority:      joint,
	})
	return nil
}

type grantMandateRequest struct {
	Type            string     `json:"type"`
	GranteeUserID   string     `json:"granteeUserId"`
	Scope           string     `json:"scope"`
	DepartmentID    *string    `json:"departmentId"`
	ParentMandateID *string    `json:"parentMandateId"`
	ValidFrom       *time.Time `json:"validFrom"`
	ValidUntil      *time.Time `json:"validUntil"`
}

func (h *Handler) grantMandate(w http.ResponseWriter, r *http.Request) error {
	var req grantMandateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	grantee, err := uuid.Parse(req.GranteeUserID)
	if err != nil {
		return badRequest("invalid_input", "granteeUserId must be a user id")
	}
	grant := MandateGrant{
		Type:          req.Type,
		GranteeUserID: grantee,
		Scope:         req.Scope,
		ValidUntil:    req.ValidUntil,
	}
	if grant.Scope == "" {
		grant.Scope = MandateScopeOrganization
	}
	if req.ValidFrom != nil {
		grant.ValidFrom = *req.ValidFrom
	}
	if req.DepartmentID != nil {
		id, perr := uuid.Parse(*req.DepartmentID)
		if perr != nil {
			return badRequest("invalid_input", "departmentId must be a department id")
		}
		grant.ScopeDepartmentID = &id
	}
	if req.ParentMandateID != nil {
		id, perr := uuid.Parse(*req.ParentMandateID)
		if perr != nil {
			return badRequest("invalid_input", "parentMandateId must be a mandate id")
		}
		grant.ParentMandateID = &id
	}

	org := OrgFromContext(r.Context())
	grantor := auth.UserFromContext(r.Context())
	mandate, err := h.store.GrantMandate(r.Context(), org.ID, grantor.ID, grant)
	if err != nil {
		return mandateError(err, "granting mandate")
	}

	respond.JSON(w, r, http.StatusCreated, mandate)
	return nil
}

type revokeMandateRequest struct {
	// EffectiveAt closes the mandate's validity window on a future date instead of
	// revoking it now, so it stays active until then and expires on its own. Absent
	// means immediate.
	EffectiveAt *time.Time `json:"effectiveAt"`
	Reason      string     `json:"reason"`
}

func (h *Handler) revokeMandate(w http.ResponseWriter, r *http.Request) error {
	mandateID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid mandate id")
	}

	var req revokeMandateRequest
	// An immediate revocation carries nothing, so an empty body is the common case
	// and not an error; anything present but unparseable still is.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) > maxRevocationReasonLength {
		return badRequest("invalid_input", "reason is too long")
	}

	org := OrgFromContext(r.Context())
	revoker := auth.UserFromContext(r.Context())
	revoked, err := h.store.RevokeMandate(r.Context(), org.ID, mandateID, revoker.ID, req.EffectiveAt, req.Reason)
	if err != nil {
		return mandateError(err, "revoking mandate")
	}

	respond.JSON(w, r, http.StatusOK, revoked)
	return nil
}

// mandateError maps the mandate sentinels to their status codes. Both mandate
// handlers share it because both can fail every way the store can: a grant is
// checked against a parent mandate, and a revocation against the same authority.
func mandateError(err error, doing string) error {
	switch {
	case errors.Is(err, ErrMandateNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "mandate_not_found", Message: "mandate not found"}
	case errors.Is(err, ErrGranteeNotMember):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "grantee_not_member", Message: "the grantee is not a member of this organization"}
	case errors.Is(err, ErrDepartmentNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "department_not_found", Message: "department not found"}
	case errors.Is(err, ErrMandateType):
		return badRequest("invalid_input", "type must be full or administrative")
	case errors.Is(err, ErrMandateScope):
		return badRequest("invalid_input", "a department-scoped mandate needs a departmentId, an organization-scoped one none")
	case errors.Is(err, ErrMandateWindow):
		return badRequest("invalid_input", "validUntil must be after validFrom")
	case errors.Is(err, ErrMandateEffectiveInPast):
		return badRequest("invalid_input", "effectiveAt must be in the future; omit it to revoke now")
	case errors.Is(err, ErrNotMandateAuthority):
		return &respond.APIError{Status: http.StatusForbidden, Code: "mandate_authority_required", Message: "you may not manage this mandate"}
	case errors.Is(err, ErrOverDelegation):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "over_delegation", Message: "a delegated mandate cannot exceed the mandate it is cut from"}
	case errors.Is(err, ErrMandateInactive):
		return &respond.APIError{Status: http.StatusConflict, Code: "mandate_inactive", Message: "this mandate is no longer active"}
	default:
		return fmt.Errorf("%s: %w", doing, err)
	}
}
