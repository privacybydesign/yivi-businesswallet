package main

import (
	"context"
	"log/slog"
	"time"
)

// startPruner runs prune on a ticker until ctx is cancelled, logging failures.
// It backs the expired-row cleanup of the session and presentation stores and
// other retention sweeps that report a count.
func startPruner(ctx context.Context, name string, every time.Duration, prune func(context.Context) (int64, error)) {
	ticker := time.NewTicker(every)
	go func() {
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

// startPrunerAtBoot is startPruner that also runs prune once at start: for a
// job whose interval is long next to how often the API is redeployed.
func startPrunerAtBoot(ctx context.Context, name string, every time.Duration, prune func(context.Context) (int64, error)) {
	go runPrune(ctx, name, prune)
	startPruner(ctx, name, every, prune)
}

func runPrune(ctx context.Context, name string, prune func(context.Context) (int64, error)) {
	if _, err := prune(ctx); err != nil {
		slog.ErrorContext(ctx, "periodic job failed",
			slog.String("store", name),
			slog.String("error", err.Error()),
		)
	}
}
