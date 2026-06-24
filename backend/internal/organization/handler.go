package organization

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/organizations/{id}", h.get)
	mux.HandleFunc("/organizations", h.list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	org, err := h.store.GetByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"id": "` + org.ID.String() + `", "name": "` + org.Name + `"}`))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.store.List(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("failed to list organizations", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`[`))
	for i, org := range orgs {
		if i > 0 {
			w.Write([]byte(`,`))
		}
		w.Write([]byte(`{"id": "` + org.ID.String() + `", "name": "` + org.Name + `"}`))
	}
	w.Write([]byte(`]`))
}
