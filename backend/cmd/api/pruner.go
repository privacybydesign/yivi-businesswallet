package main

import (
	"context"
	"log/slog"
	"time"
)

// startPruner runs prune on a ticker until ctx is cancelled, logging failures.
// It backs the expired-row cleanup for both the session and presentation stores.
func startPruner(ctx context.Context, name string, every time.Duration, prune func(context.Context) (int64, error)) {
	ticker := time.NewTicker(every)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := prune(ctx); err != nil {
					slog.ErrorContext(ctx, "prune failed",
						slog.String("store", name),
						slog.String("error", err.Error()),
					)
				}
			}
		}
	}()
}
