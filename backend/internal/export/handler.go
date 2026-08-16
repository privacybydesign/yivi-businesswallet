package export

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type exporter interface {
	Export(ctx context.Context, org Organization, sections []string) (*Archive, error)
}

// Handler serves the data-portability download. The bundle carries every
// member's personal data, so it is org-admin only and audited.
type Handler struct {
	service     exporter
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(service exporter, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/export", admin(respond.HandlerFunc(h.export)))
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())

	archive, err := h.service.Export(r.Context(), owner(org), ParseSections(r.URL.Query().Get("sections")))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := archive.Close(); closeErr != nil {
			slog.ErrorContext(r.Context(), "export: cleaning up bundle",
				slog.String("error", closeErr.Error()))
		}
	}()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archive.Filename))
	w.Header().Set("Content-Length", strconv.FormatInt(archive.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, archive.Reader()); err != nil {
		slog.ErrorContext(r.Context(), "export: writing bundle body",
			slog.String("error", err.Error()))
	}
	return nil
}

func owner(org organization.Organization) Organization {
	return Organization{
		ID:             org.ID,
		Name:           org.Name,
		Slug:           org.Slug,
		KVKNumber:      org.KVKNumber,
		EUID:           org.EUID,
		DigitalAddress: org.DigitalAddress,
		Status:         org.Status,
		BootstrappedAt: timestamp(org.BootstrappedAt),
	}
}
