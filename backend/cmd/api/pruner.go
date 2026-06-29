package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
)

func startSessionPruner(ctx context.Context, store *session.Store, every time.Duration) {
	ticker := time.NewTicker(every)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := store.DeleteExpired(ctx); err != nil {
					slog.ErrorContext(ctx, "session prune failed",
						slog.String("error", err.Error()),
					)
				}
			}
		}
	}()
}
