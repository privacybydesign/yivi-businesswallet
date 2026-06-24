package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: migrate <up|down|version>")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logging.Setup(cfg.LogLevel, cfg.LogFormat, cfg.LogSource)

	ctx := context.Background()
	cmd := os.Args[1]

	switch cmd {
	case "up":
		slog.Info("running migrations up")
		if err := migrate.Up(ctx, cfg.DatabaseDSN); err != nil {
			return err
		}
		slog.Info("migrations up complete")
		return nil
	case "down":
		slog.Info("running migrations down")
		if err := migrate.Down(ctx, cfg.DatabaseDSN); err != nil {
			return err
		}
		slog.Info("migrations down complete")
		return nil
	case "version":
		v, err := migrate.Version(ctx, cfg.DatabaseDSN)
		if err != nil {
			return err
		}
		slog.Info("current version", slog.Int64("version", v))
		return nil
	default:
		return errors.New("unknown command: " + cmd)
	}
}
