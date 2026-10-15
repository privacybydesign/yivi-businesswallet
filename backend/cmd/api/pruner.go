package main

import (
	"context"
	"log/slog"
	"time"
)

// startPruner runs prune once at start and then every interval until ctx is
// cancelled, logging failures. It backs the expired-row cleanup of the session
// and presentation stores and other retention sweeps that report a count. The
// run at start matters for retention: a replica restarted more often than
// every would otherwise never prune at all.
func startPruner(ctx context.Context, name string, every time.Duration, prune func(context.Context) (int64, error)) {
	go func() {
		runPrune(ctx, name, prune)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runPrune(ctx, name, prune)
			}
		}
	}()
}

func runPrune(ctx context.Context, name string, prune func(context.Context) (int64, error)) {
	if _, err := prune(ctx); err != nil {
		slog.ErrorContext(ctx, "periodic job failed",
			slog.String("store", name),
			slog.String("error", err.Error()),
		)
	}
}
