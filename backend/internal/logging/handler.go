package logging

import (
	"context"
	"log/slog"
)

// attrKeyRequestID is the log attribute key for request correlation.
const attrKeyRequestID = "request_id"

// contextHandler is an slog.Handler wrapper that reads contextual attributes
// (e.g. request ID) from the context passed to *Context logging methods and
// adds them to the log record before delegating to the inner handler.
//
// Invariant: contextual attributes are added at top level. If a caller uses
// WithGroup before logging, the attributes will nest inside that group. Keep
// contextual attribute injection (middleware) before any WithGroup use so that
// request_id stays at top level for queryability.
type contextHandler struct {
	inner slog.Handler
}

// NewContextHandler wraps inner so that contextual attributes stored via
// [ContextWithRequestID] are automatically included in every log record.
func NewContextHandler(inner slog.Handler) *contextHandler {
	return &contextHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle adds contextual attributes from ctx to the record, then delegates.
func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String(attrKeyRequestID, id))
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs delegates to the inner handler, preserving the wrapper.
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup delegates to the inner handler, preserving the wrapper.
func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}
