package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/seed"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logging.Setup(cfg.LogLevel, cfg.LogFormat, cfg.LogSource)

	ctx := context.Background()

	slog.Info("running database seed")
	if err := seed.Run(ctx, cfg.DatabaseDSN); err != nil {
		return err
	}
	slog.Info("database seed complete")

	return nil
}
