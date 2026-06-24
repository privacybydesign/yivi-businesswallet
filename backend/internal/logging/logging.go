package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

const (
	FormatJSON = "json"
	FormatText = "text"
)

// contextKey prevents collisions with context value keys from other packages.
type contextKey int

const requestIDKey contextKey = iota

func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

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
