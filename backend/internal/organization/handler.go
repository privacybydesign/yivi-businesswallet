package organization

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /organizations/{id}", respond.HandlerFunc(h.get))
	mux.Handle("GET /organizations", respond.HandlerFunc(h.list))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return &respond.APIError{
			Status:  http.StatusBadRequest,
			Code:    "invalid_id",
			Message: "invalid id",
		}
	}

	org, err := h.store.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{
			Status:  http.StatusNotFound,
			Code:    "org_not_found",
			Message: "organization not found",
		}
	}
	if err != nil {
		return fmt.Errorf("getting organization %d: %w", id, err)
	}

	respond.JSON(w, r, http.StatusOK, org)
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	orgs, err := h.store.List(r.Context())
	if err != nil {
		return fmt.Errorf("listing organizations: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, orgs)
	return nil
}
