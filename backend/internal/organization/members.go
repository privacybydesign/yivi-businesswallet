package organization

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func (h *Handler) members(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	members, err := h.store.ListMembers(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing members: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, members)
	return nil
}

type inviteRequest struct {
	Email        string  `json:"email"`
	GivenNames   string  `json:"givenNames"`
	LastName     string  `json:"lastName"`
	Role         string  `json:"role"`
	JobTitle     *string `json:"jobTitle"`
	DepartmentID *string `json:"departmentId"`
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) error {
	var req inviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	givenNames := strings.TrimSpace(req.GivenNames)
	lastName := strings.TrimSpace(req.LastName)
	if strings.TrimSpace(req.Email) == "" || givenNames == "" || lastName == "" {
		return badRequest("invalid_input", "email, givenNames, and lastName are required")
	}
	email, err := user.ParseEmail(req.Email)
	if err != nil {
		return badRequest("invalid_email", "email is not valid")
	}

	role := req.Role
	if role == "" {
		role = RoleMember
	}
	if role != RoleMember && role != RoleAdmin {
		return badRequest("invalid_role", "role must be member or admin")
	}

	var deptID *uuid.UUID
	if req.DepartmentID != nil {
		id, err := uuid.Parse(*req.DepartmentID)
		if err != nil {
			return badRequest("invalid_department", "invalid department id")
		}
		deptID = &id
	}

	org := OrgFromContext(r.Context())
	invitation, err := h.service.InviteMember(r.Context(), org.ID, Invite{
		Email:        email,
		GivenNames:   givenNames,
		LastName:     lastName,
		Role:         role,
		JobTitle:     normalize(req.JobTitle),
		DepartmentID: deptID,
		InvitedBy:    auth.UserFromContext(r.Context()).ID,
	})
	switch {
	case errors.Is(err, ErrAlreadyMember):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_member", Message: "user is already a member of this organization"}
	case errors.Is(err, ErrAlreadyInvited):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_invited", Message: "user is already invited to this organization"}
	case errors.Is(err, ErrDepartmentNotFound):
		return badRequest("department_not_found", "department not found")
	case err != nil:
		return fmt.Errorf("inviting member: %w", err)
	}

	respond.JSON(w, r, http.StatusCreated, invitation)
	return nil
}

type updateMemberRequest struct {
	Role         *string `json:"role"`
	JobTitle     *string `json:"jobTitle"`
	DepartmentID *string `json:"departmentId"`
}

func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}

	var req updateMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	if req.Role != nil && *req.Role != RoleMember && *req.Role != RoleAdmin {
		return badRequest("invalid_role", "role must be member or admin")
	}

	var deptID *uuid.UUID
	if req.DepartmentID != nil {
		id, err := uuid.Parse(*req.DepartmentID)
		if err != nil {
			return badRequest("invalid_department", "invalid department id")
		}
		deptID = &id
	}

	org := OrgFromContext(r.Context())
	member, err := h.store.UpdateMembership(r.Context(), org.ID, userID, req.Role, normalize(req.JobTitle), deptID)
	switch {
	case errors.Is(err, ErrNotMember):
		return &respond.APIError{Status: http.StatusNotFound, Code: "member_not_found", Message: "member not found"}
	case errors.Is(err, ErrLastAdmin):
		return &respond.APIError{Status: http.StatusConflict, Code: "last_admin", Message: "cannot demote the last admin of the organization"}
	case errors.Is(err, ErrDepartmentNotFound):
		return badRequest("department_not_found", "department not found")
	case err != nil:
		return fmt.Errorf("updating member: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, member)
	return nil
}

// normalize trims a job title and maps empty/whitespace to nil so the column
// stays NULL rather than storing "".
func normalize(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
