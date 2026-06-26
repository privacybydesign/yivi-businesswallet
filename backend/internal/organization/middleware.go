package organization

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Authorize resolves the {slug} organization, authorizes the caller (platform
// admin, else a member), and stashes the org and effective role in context. It
// must be composed inside auth.RequireUser, which puts the user in context.
func (h *Handler) Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		u := auth.UserFromContext(ctx)

		org, err := h.store.GetBySlug(ctx, r.PathValue("slug"))
		if errors.Is(err, ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, "org_not_found", "organization not found")
			return
		}
		if err != nil {
			slog.ErrorContext(ctx, "resolving organization", slog.String("error", err.Error()))
			respond.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}

		role := RoleAdmin
		if !h.admins.Has(u.Email) {
			m, err := h.store.GetMembership(ctx, u.ID, org.ID)
			if errors.Is(err, ErrNotMember) {
				respond.Error(w, r, http.StatusForbidden, "forbidden", "forbidden")
				return
			}
			if err != nil {
				slog.ErrorContext(ctx, "resolving membership", slog.String("error", err.Error()))
				respond.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
			role = m.Role
		}

		ctx = contextWithRole(contextWithOrg(ctx, org), role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireOrgAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if roleFromContext(r.Context()) != RoleAdmin {
			respond.Error(w, r, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
