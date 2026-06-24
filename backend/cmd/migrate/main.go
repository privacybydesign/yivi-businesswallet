package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "error", err)
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

	ctx := context.Background()
	switch os.Args[1] {
	case "up":
		return migrate.Up(ctx, cfg.DatabaseDSN)
	case "down":
		return migrate.Down(ctx, cfg.DatabaseDSN)
	case "version":
		v, err := migrate.Version(ctx, cfg.DatabaseDSN)
		if err != nil {
			return err
		}
		slog.Info("current version", "version", v)
		return nil
	default:
		return errors.New("unknown command: " + os.Args[1])
	}
}
