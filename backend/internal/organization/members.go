package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type memberListPage struct {
	Entries []MemberEntry `json:"entries"`
	Total   int           `json:"total"`
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	status := q.Get("status")
	if status != "" && status != StatusActive && status != StatusInvited {
		return badRequest("invalid_status", `status must be "active" or "invited"`)
	}

	sort := q.Get("sort")
	if sort == "" {
		sort = defaultMemberSort
	} else if _, ok := memberSortColumns[sort]; !ok {
		return badRequest("invalid_sort", "invalid sort column")
	}

	var desc bool
	switch q.Get("dir") {
	case "", "asc":
		desc = false
	case "desc":
		desc = true
	default:
		return badRequest("invalid_dir", `dir must be "asc" or "desc"`)
	}

	limit := DefaultMemberListLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return badRequest("invalid_limit", "limit must be a positive integer")
		}
		if n > MaxMemberListLimit {
			n = MaxMemberListLimit
		}
		limit = n
	}

	offset := 0
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return badRequest("invalid_offset", "offset must be a non-negative integer")
		}
		offset = n
	}

	org := OrgFromContext(r.Context())
	entries, total, err := h.store.ListMemberEntries(r.Context(), org.ID, MemberListParams{
		Status: status,
		Search: strings.TrimSpace(q.Get("q")),
		Sort:   sort,
		Desc:   desc,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return fmt.Errorf("listing members: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, memberListPage{Entries: entries, Total: total})
	return nil
}

func (h *Handler) member(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}

	org := OrgFromContext(r.Context())
	member, err := h.store.GetMember(r.Context(), org.ID, userID)
	switch {
	case errors.Is(err, ErrNotMember):
		return &respond.APIError{Status: http.StatusNotFound, Code: "member_not_found", Message: "member not found"}
	case err != nil:
		return fmt.Errorf("getting member: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, member)
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
	inv, err := h.service.InviteMember(r.Context(), org.ID, Invite{
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

	h.sendInviteEmail(r.Context(), org, inv)

	w.WriteHeader(http.StatusCreated)
	return nil
}

// sendInviteEmail delivers the invitation e-mail best-effort: the invitation is
// already persisted and discoverable in-app, so a delivery failure (including an
// org with no SMTP configured) is logged, never fatal.
func (h *Handler) sendInviteEmail(ctx context.Context, org Organization, inv Invitation) {
	if h.mailer == nil {
		return
	}
	acceptURL := h.appBaseURL + "/invite/" + inv.Token
	if err := h.mailer.SendInvitation(ctx, org.ID, inv.Email, org.Name, acceptURL); err != nil {
		slog.WarnContext(ctx, "invitation e-mail not sent",
			slog.String("email", inv.Email), slog.Any("error", err))
	}
}

func (h *Handler) resendInvitation(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid invitation id")
	}
	org := OrgFromContext(r.Context())
	inv, err := h.store.ResendInvitation(r.Context(), org.ID, id)
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "invitation_not_found", Message: "invitation not found"}
	case err != nil:
		return fmt.Errorf("resending invitation: %w", err)
	}
	h.sendInviteEmail(r.Context(), org, inv)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid invitation id")
	}
	org := OrgFromContext(r.Context())
	switch err := h.store.RevokeInvitation(r.Context(), org.ID, id); {
	case errors.Is(err, ErrInvitationNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "invitation_not_found", Message: "invitation not found"}
	case err != nil:
		return fmt.Errorf("revoking invitation: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
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
	_, err = h.store.UpdateMembership(r.Context(), org.ID, userID, req.Role, normalize(req.JobTitle), deptID)
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

	w.WriteHeader(http.StatusNoContent)
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

// nullIfEmpty maps an empty string to nil so an absent value stores as SQL NULL
// rather than an empty string (which the UI would render as a blank field).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
