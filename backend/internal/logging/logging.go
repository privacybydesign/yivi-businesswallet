// Package logging provides centralized slog setup and request-scoped context
// helpers for structured logging.
//
// Call [Setup] once at program start (after config.Load) to configure the
// global slog.Default logger. Use [ContextWithRequestID] in HTTP middleware to
// attach a request ID, and the context-aware handler (see handler.go) will
// automatically include it in every log record produced via slog.*Context
// methods.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Supported LOG_FORMAT values.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// contextKey is an unexported type used for context value keys, preventing
// collisions with keys defined in other packages.
type contextKey int

const requestIDKey contextKey = iota

// ContextWithRequestID returns a child context carrying the given request ID.
// The context-aware handler reads it back via [RequestIDFromContext].
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext extracts the request ID stored by
// [ContextWithRequestID]. Returns an empty string when no ID is present.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Setup configures the global slog.Default logger based on the provided
// level, format, and source settings. It writes to os.Stdout (12-factor).
//
// level is one of "debug", "info", "warn", "error" (case-insensitive);
// unknown values default to info.
//
// format is "json" or "text" (case-insensitive); unknown values default to
// text.
//
// source controls whether the source file location is included in every log
// record (via runtime.Callers). Defaults to true. Set LOG_SOURCE=false to
// disable in high-throughput production paths.
func Setup(level, format string, source bool) {
	opts := &slog.HandlerOptions{
		Level:     parseLevel(level),
		AddSource: source,
	}

	var base slog.Handler
	switch strings.ToLower(format) {
	case FormatJSON:
		base = slog.NewJSONHandler(os.Stdout, opts)
	default:
		base = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(NewContextHandler(base)))
}

// parseLevel maps a human-readable level string to a slog.Level.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
