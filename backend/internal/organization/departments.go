package organization

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func (h *Handler) listDepartments(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	departments, err := h.store.ListDepartments(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing departments: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, departments)
	return nil
}

type departmentRequest struct {
	Name string `json:"name"`
}

func (h *Handler) createDepartment(w http.ResponseWriter, r *http.Request) error {
	var req departmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest("invalid_input", "name is required")
	}

	org := OrgFromContext(r.Context())
	if _, err := h.store.CreateDepartment(r.Context(), org.ID, req.Name); errors.Is(err, ErrDepartmentNameTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "name_taken", Message: "department name already taken"}
	} else if err != nil {
		return fmt.Errorf("creating department: %w", err)
	}

	w.WriteHeader(http.StatusCreated)
	return nil
}

func (h *Handler) updateDepartment(w http.ResponseWriter, r *http.Request) error {
	deptID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid department id")
	}

	var req departmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest("invalid_input", "name is required")
	}

	org := OrgFromContext(r.Context())
	_, err = h.store.UpdateDepartment(r.Context(), org.ID, deptID, req.Name)
	switch {
	case errors.Is(err, ErrDepartmentNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "department_not_found", Message: "department not found"}
	case errors.Is(err, ErrDepartmentNameTaken):
		return &respond.APIError{Status: http.StatusConflict, Code: "name_taken", Message: "department name already taken"}
	case err != nil:
		return fmt.Errorf("updating department: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) deleteDepartment(w http.ResponseWriter, r *http.Request) error {
	deptID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid department id")
	}

	org := OrgFromContext(r.Context())
	err = h.store.DeleteDepartment(r.Context(), org.ID, deptID)
	switch {
	case errors.Is(err, ErrDepartmentNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "department_not_found", Message: "department not found"}
	case errors.Is(err, ErrDepartmentInUse):
		return &respond.APIError{Status: http.StatusConflict, Code: "department_in_use", Message: "department still has members"}
	case err != nil:
		return fmt.Errorf("deleting department: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
