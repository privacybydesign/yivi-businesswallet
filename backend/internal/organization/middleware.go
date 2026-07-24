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

// RequirePermission gates a route on a single {resource}:{action} permission
// from the RBAC matrix (permissions.go). It reads the effective role stashed by
// Authorize — never the request — resolves it against the compiled role ->
// permission map, and forbids the request when the permission is absent.
//
// This is the enforcement seam #27 extends: v1 checks role -> permission with
// org-wide scope and no validity window, so the shape stays fixed when scope
// narrowing, validity windows and Axis-A (mandate) checks are added behind it.
func RequirePermission(resource, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasPermission(roleFromContext(r.Context()), resource, action) {
				respond.Error(w, r, http.StatusForbidden, "forbidden", "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireOrgAdmin gates on the administrative-mandate role. It is retained as a
// thin alias over the RBAC layer while the remaining slices migrate to
// RequirePermission (rbac-model.md); the routes still on it (wallet lifecycle,
// org settings, department structure) either have no distinct functional role
// or are Axis-A-gated and stay admin-only.
func RequireOrgAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if roleFromContext(r.Context()) != RoleAdmin {
			respond.Error(w, r, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
