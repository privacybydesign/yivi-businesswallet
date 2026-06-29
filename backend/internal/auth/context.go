package auth

import (
	"context"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type ctxKey struct{}

func ContextWithUser(ctx context.Context, u user.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFromContext returns the user injected by RequireUser. The bare assertion
// panics if absent, so only call it from handlers wrapped by RequireUser.
func UserFromContext(ctx context.Context) user.User {
	return ctx.Value(ctxKey{}).(user.User)
}
