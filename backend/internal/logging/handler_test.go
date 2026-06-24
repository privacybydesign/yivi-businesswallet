package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"
)

func TestContextHandler_Conformance(t *testing.T) {
	var buf bytes.Buffer

	slogtest.Run(t, func(t *testing.T) slog.Handler {
		t.Helper()
		buf.Reset()
		inner := slog.NewJSONHandler(&buf, nil)
		return NewContextHandler(inner)
	}, func(t *testing.T) map[string]any {
		t.Helper()

		line := buf.Bytes()
		if len(line) == 0 {
			return nil
		}

		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("failed to parse log output: %v\nraw: %s", err, line)
		}
		return m
	})
}

func TestContextHandler_RequestID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewContextHandler(inner)
	logger := slog.New(handler)

	ctx := ContextWithRequestID(context.Background(), "test-id-123")
	logger.InfoContext(ctx, "hello")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	got, ok := record[attrKeyRequestID]
	if !ok {
		t.Fatal("expected request_id in log output, got none")
	}
	if got != "test-id-123" {
		t.Fatalf("expected request_id=test-id-123, got %v", got)
	}
}

func TestContextHandler_NoRequestID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewContextHandler(inner)
	logger := slog.New(handler)

	logger.InfoContext(context.Background(), "hello")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if _, ok := record[attrKeyRequestID]; ok {
		t.Fatal("request_id should not be present when no ID is in context")
	}
}

// Verifies WithAttrs returns a handler that still injects request_id from
// context (i.e. the wrapper is preserved, not unwrapped).
func TestContextHandler_WithAttrsPreservesWrapper(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := NewContextHandler(inner)

	child := handler.WithAttrs([]slog.Attr{slog.String("component", "test")})
	logger := slog.New(child)

	ctx := ContextWithRequestID(context.Background(), "attrs-test")
	logger.InfoContext(ctx, "with attrs")

	output := buf.String()
	if !strings.Contains(output, "attrs-test") {
		t.Fatalf("expected request_id in output after WithAttrs, got: %s", output)
	}
	if !strings.Contains(output, "test") {
		t.Fatalf("expected component attr in output after WithAttrs, got: %s", output)
	}
}
