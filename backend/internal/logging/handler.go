package logging

import (
	"context"
	"log/slog"
)

const attrKeyRequestID = "request_id"

// Invariant: contextual attributes are added at top level. If a caller uses
// WithGroup before logging, request_id will nest inside that group and become
// hard to query. Keep contextual attribute injection (middleware) before any
// WithGroup use.
type contextHandler struct {
	inner slog.Handler
}

func NewContextHandler(inner slog.Handler) *contextHandler {
	return &contextHandler{inner: inner}
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String(attrKeyRequestID, id))
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}
