package organization

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Authorize resolves the {slug} organization, authorizes the caller (platform
// admin, else a member), and stashes the org, effective role and basis of
// authority in context. It must be composed inside auth.RequireUser, which puts
// the user in context.
//
// The authority lookup is what makes the mandate layer real-time: it is read
// fresh on every request, so revoking or expiring a mandate takes effect on the
// next one rather than whenever a sweep gets to it (Annex §12(3)(b)).
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
		platformAdmin := h.admins.Has(u.Email)
		if !platformAdmin {
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

		authority, err := h.store.ResolveAuthority(ctx, org.ID, u.ID)
		if err != nil {
			slog.ErrorContext(ctx, "resolving authority", slog.String("error", err.Error()))
			respond.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		authority.PlatformAdmin = platformAdmin

		ctx = contextWithAuthority(contextWithRole(contextWithOrg(ctx, org), role), authority)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOrgAdmin gates the administrative surface. The caller's functional role
// must be admin and their administrative authority must not have been withdrawn:
// once an organization starts granting mandates, an admin whose mandates are all
// revoked, expired, not yet in force, or narrowed to one department stops being an
// admin org-wide, without a second write to the membership row. An organization
// that has never granted a mandate is unaffected.
func RequireOrgAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if roleFromContext(ctx) != RoleAdmin {
			respond.Error(w, r, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
		if AuthorityFromContext(ctx).Withdrawn() {
			respond.Error(w, r, http.StatusForbidden, "mandate_withdrawn",
				"your mandate for this organization is no longer active")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireMandateAuthority gates granting and revoking mandates on Axis A: the
// caller must be a register-backed legal representative or hold an active
// org-wide full mandate. No functional role reaches it, so an admin cannot mint
// itself a mandate (rbac-model.md, "Axis B: functional roles").
func RequireMandateAuthority(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !AuthorityFromContext(r.Context()).MayGrantMandate() {
			respond.Error(w, r, http.StatusForbidden, "mandate_authority_required",
				"only a legal representative or a full-mandate holder may manage mandates")
			return
		}
		next.ServeHTTP(w, r)
	})
}
