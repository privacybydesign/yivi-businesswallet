package main

import (
	"context"
	"flag"
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
	adminsOnly := flag.Bool("admins", false,
		"provision only the configured PLATFORM_ADMIN_EMAILS accounts (no demo data); safe for staging/production")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logging.Setup(cfg.LogLevel, cfg.LogFormat, cfg.LogSource)

	ctx := context.Background()

	if *adminsOnly {
		slog.Info("provisioning platform-admin accounts", slog.Int("count", len(cfg.PlatformAdminEmails)))
		if err := seed.EnsurePlatformAdmins(ctx, cfg.DatabaseDSN, cfg.PlatformAdminEmails); err != nil {
			return err
		}
		slog.Info("platform-admin provisioning complete")
		return nil
	}

	slog.Info("running database seed")
	if err := seed.Run(ctx, cfg.DatabaseDSN); err != nil {
		return err
	}
	slog.Info("database seed complete")

	return nil
}
