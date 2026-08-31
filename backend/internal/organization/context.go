package organization

import "context"

type (
	orgCtxKey       struct{}
	roleCtxKey      struct{}
	authorityCtxKey struct{}
)

func contextWithOrg(ctx context.Context, org Organization) context.Context {
	return context.WithValue(ctx, orgCtxKey{}, org)
}

func contextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleCtxKey{}, role)
}

func OrgFromContext(ctx context.Context) Organization {
	return ctx.Value(orgCtxKey{}).(Organization)
}

// ContextWithOrg returns ctx carrying org. The authorize middleware wires the
// resolved org this way; it is exported so handler tests in other packages can
// inject an org the same way OrgFromContext reads it.
func ContextWithOrg(ctx context.Context, org Organization) context.Context {
	return contextWithOrg(ctx, org)
}

// ContextWithRole returns ctx carrying the caller's effective role in the
// resolved org, the companion of ContextWithOrg: a handler test in another
// package that composes RequireOrgAdmin needs both, since the middleware that
// normally sets them is this package's.
func ContextWithRole(ctx context.Context, role string) context.Context {
	return contextWithRole(ctx, role)
}

func roleFromContext(ctx context.Context) string {
	role, _ := ctx.Value(roleCtxKey{}).(string)
	return role
}

// RoleFromContext returns the caller's effective role in the resolved org, as set
// by the Authorize middleware. Exported so a member-gated handler in another
// package can distinguish an admin caller from an ordinary member (e.g. to widen
// access to any org record) without a second membership lookup.
func RoleFromContext(ctx context.Context) string {
	return roleFromContext(ctx)
}

// IsAdmin reports whether the caller's effective role in the resolved org is admin.
func IsAdmin(ctx context.Context) bool {
	return roleFromContext(ctx) == RoleAdmin
}

func contextWithAuthority(ctx context.Context, a Authority) context.Context {
	return context.WithValue(ctx, authorityCtxKey{}, a)
}

// ContextWithAuthority returns ctx carrying the caller's basis of authority, the
// third companion of ContextWithOrg and ContextWithRole: a handler test in another
// package that composes RequireOrgAdmin needs it too, since the middleware that
// normally sets it is this package's.
func ContextWithAuthority(ctx context.Context, a Authority) context.Context {
	return contextWithAuthority(ctx, a)
}

// AuthorityFromContext returns the caller's basis of authority in the resolved
// org, as set by the Authorize middleware. The zero Authority — no mandates
// granted, so nothing withdrawn and no mandate authority — is what a caller
// outside the org-scoped chain sees, which is why every field is false-or-zero
// safe.
func AuthorityFromContext(ctx context.Context) Authority {
	a, _ := ctx.Value(authorityCtxKey{}).(Authority)
	return a
}
