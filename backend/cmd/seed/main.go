package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/seed"
)

// envSeedEnvironment names where a seed runs. --proofing-demo creates demo
// organisations named after real companies, so it runs only where this is
// seedEnvironmentStaging, or on a local stack (DEV_MODE).
const (
	envSeedEnvironment     = "SEED_ENVIRONMENT"
	seedEnvironmentStaging = "staging"
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
	orgOnly := flag.Bool("org", false,
		"provision the anchor organisations — Yivi (with its team as admins + attestation catalogue) and the KVK register (authentic source); no demo members or activity, safe for staging/production")
	partnersOnly := flag.Bool("partners", false,
		"provision the staging pilot partner organisations (Anoigo, Gemeente Nijmegen, Ver.iD, PinkRoccade, Stichting Nuts, Secumail) with their teams as admins — Gemeente Nijmegen also gets its APV standplaatsvergunning attestation catalogue; no other demo data, idempotent — staging only, not for production")
	proofingDemo := flag.Bool("proofing-demo", false,
		"provision the identity proofing demo orgs (Radboud, a.s.r., Unibet, CM) with their flows and customers, and the Yivi team as their admins; idempotent; staging only: needs SEED_ENVIRONMENT=staging, or DEV_MODE")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logging.Setup(cfg.LogLevel, cfg.LogFormat, cfg.LogSource)

	if *proofingDemo {
		if err := checkProofingDemo(cfg.DevMode, os.Getenv(envSeedEnvironment)); err != nil {
			return err
		}
	}

	ctx := context.Background()

	// The partial seeds are idempotent, so they can be combined and run on
	// every deploy of the environment they are for: --admins and --org on
	// staging and production, --partners and --proofing-demo on staging only.
	// Only when no flag is set does the full dev demo seed run.
	if *adminsOnly || *orgOnly || *partnersOnly || *proofingDemo {
		if *adminsOnly {
			slog.Info("provisioning platform-admin accounts", slog.Int("count", len(cfg.PlatformAdminEmails)))
			if err := seed.EnsurePlatformAdmins(ctx, cfg.DatabaseDSN, cfg.PlatformAdminEmails); err != nil {
				return err
			}
			slog.Info("platform-admin provisioning complete")
		}
		if *orgOnly {
			slog.Info("provisioning Yivi organisation")
			if _, err := seed.EnsureYiviOrganization(ctx, cfg.DatabaseDSN, cfg.QerdsDefaultAddressDomain); err != nil {
				return err
			}
			slog.Info("Yivi organisation provisioning complete")

			slog.Info("provisioning KVK register organisation")
			if _, err := seed.EnsureKVKRegisterOrganization(ctx, cfg.DatabaseDSN, cfg.QerdsDefaultAddressDomain); err != nil {
				return err
			}
			slog.Info("KVK register organisation provisioning complete")
		}
		if *partnersOnly {
			slog.Info("provisioning staging partner organisations")
			if err := seed.EnsurePartnerOrganizations(ctx, cfg.DatabaseDSN, cfg.QerdsDefaultAddressDomain); err != nil {
				return err
			}
			slog.Info("partner organisation provisioning complete")
		}
		if *proofingDemo {
			slog.Info("provisioning identity proofing demo")
			if err := seed.EnsureProofingDemo(ctx, cfg.DatabaseDSN, cfg.QerdsDefaultAddressDomain); err != nil {
				return err
			}
			slog.Info("identity proofing demo provisioning complete")
		}
		return nil
	}

	slog.Info("running database seed")
	if err := seed.Run(ctx, cfg.DatabaseDSN, cfg.QerdsDefaultAddressDomain, cfg.PlatformAdminEmails); err != nil {
		return err
	}
	slog.Info("database seed complete")

	return nil
}

// checkProofingDemo refuses --proofing-demo outside staging (environment) and
// a local stack (devMode).
func checkProofingDemo(devMode bool, environment string) error {
	if devMode || environment == seedEnvironmentStaging {
		return nil
	}
	return fmt.Errorf("--proofing-demo is for staging only: set %s=%s, or run it on a local stack (DEV_MODE)",
		envSeedEnvironment, seedEnvironmentStaging)
}
