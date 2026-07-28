package organization

import "context"

type (
	orgCtxKey  struct{}
	roleCtxKey struct{}
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
