package main

import (
	"context"
	"log/slog"
	"time"
)

// startQerdsPoller runs poll on a ticker until ctx is cancelled, backing the
// QERDS inbound poll/both delivery modes. A poll failure is logged, not fatal:
// the next tick retries, and intake is idempotent (dedupe on provider ref).
func startQerdsPoller(ctx context.Context, every time.Duration, poll func(context.Context) (int, error)) {
	ticker := time.NewTicker(every)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				received, err := poll(ctx)
				if err != nil {
					slog.ErrorContext(ctx, "qerds poll worker failed", slog.String("error", err.Error()))
					continue
				}
				if received > 0 {
					slog.InfoContext(ctx, "qerds poll worker stored inbound messages", slog.Int("received", received))
				}
			}
		}
	}()
}
