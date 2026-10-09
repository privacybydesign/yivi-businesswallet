package organization

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// terminate ends service for an organisation (Art 7(6)(f)) and queues the export
// it owes. Platform-admin only: this is the provider acting, not the owner.
func (h *Handler) terminate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid organization id")
	}
	if h.exports == nil {
		// Terminating without producing the bundle would end service and drop the
		// handover obligation on the floor.
		return &respond.APIError{
			Status:  http.StatusServiceUnavailable,
			Code:    "export_unavailable",
			Message: "termination needs the export service",
		}
	}

	org, err := h.store.Terminate(r.Context(), id, h.exports)
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "organization not found"}
	}
	if errors.Is(err, ErrAlreadyTerminated) {
		return &respond.APIError{
			Status:  http.StatusConflict,
			Code:    "already_terminated",
			Message: "service was already terminated for this organization",
		}
	}
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, org)
	return nil
}

// setDataInstruction records the owner's standing instruction for their data on
// termination. Org-admin only, and captured in advance because termination is
// exactly the moment nobody can be asked.
func (h *Handler) setDataInstruction(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		DataInstruction string `json:"dataInstruction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := OrgFromContext(r.Context())
	updated, err := h.store.SetDataInstruction(r.Context(), org.ID, body.DataInstruction)
	if errors.Is(err, ErrInvalidInstruction) {
		return badRequest("invalid_input", "dataInstruction must be transfer or delete")
	}
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "organization not found"}
	}
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, updated)
	return nil
}
