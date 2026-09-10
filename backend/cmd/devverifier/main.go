// Command devverifier is a local OpenID4VP relying party for driving the
// business wallet's inbound presentation flow end to end without a hosted
// verifier: it signs an Authorization Request the wallet fetches, prints the
// link that invokes the wallet, receives the Authorization Response and shows
// what the organization disclosed, verified. Dev stack only (compose profile
// "verifier"); see .ai/features/openid4vp-inbound.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
)

const (
	envListen           = "DEVVERIFIER_LISTEN"
	envPublicURL        = "DEVVERIFIER_PUBLIC_URL"
	envInternalURL      = "DEVVERIFIER_INTERNAL_URL"
	envWalletURL        = "DEVVERIFIER_WALLET_URL"
	envChainFile        = "DEVVERIFIER_CHAIN_FILE"
	envKeyFile          = "DEVVERIFIER_KEY_FILE"
	envDNSName          = "DEVVERIFIER_DNS_NAME"
	envIssuerTrustChain = "DEVVERIFIER_ISSUER_TRUST_CHAIN"
	envStagingAnchors   = "DEVVERIFIER_STAGING_ANCHORS"
	// envWriteChainDir mints a fresh identity for DNS name, writes it to the
	// directory as the three PEM files the Compose stack mounts, and exits — how
	// dev-setup/devverifier/ is (re)generated.
	envWriteChainDir = "DEVVERIFIER_WRITE_CHAIN_DIR"

	chainFileName = "devverifier-chain.pem"
	keyFileName   = "devverifier-key.pem"
	rootFileName  = "devverifier-root.pem"
	keyFileMode   = 0o600
	certFileMode  = 0o644

	defaultListen    = ":8090"
	defaultPublicURL = "http://localhost:8090"
	defaultWalletURL = "http://localhost:5173"
	defaultDNSName   = "devverifier"

	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("devverifier failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	logging.Setup("info", "text", false)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if dir := os.Getenv(envWriteChainDir); dir != "" {
		return writeChain(dir, envOr(envDNSName, defaultDNSName))
	}

	publicURL := strings.TrimRight(envOr(envPublicURL, defaultPublicURL), "/")
	internalURL := strings.TrimRight(envOr(envInternalURL, publicURL), "/")

	identity, err := loadOrMintIdentity()
	if err != nil {
		return err
	}
	// The wallet must trust this root: with checked-in certificates it is
	// already in OPENID4VP_VERIFIER_TRUST_CHAIN; with an ephemeral one the
	// operator pastes it from this log.
	fmt.Printf("dev verifier client_id: %s\nroot certificate (OPENID4VP_VERIFIER_TRUST_CHAIN):\n%s", identity.ClientID(), identity.RootPEM())

	issuerTrust, err := eudiholder.NewIssuerTrust([]byte(os.Getenv(envIssuerTrustChain)), !strings.EqualFold(os.Getenv(envStagingAnchors), "false"))
	if err != nil {
		return err
	}

	srv := newServer(identity, issuerTrust, publicURL, internalURL, strings.TrimRight(envOr(envWalletURL, defaultWalletURL), "/"))
	httpServer := &http.Server{
		Addr:              envOr(envListen, defaultListen),
		Handler:           srv.handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	slog.Info("dev verifier listening", slog.String("addr", httpServer.Addr), slog.String("publicUrl", publicURL), slog.String("internalUrl", internalURL))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func loadOrMintIdentity() (devverifier.Identity, error) {
	chain, key := os.Getenv(envChainFile), os.Getenv(envKeyFile)
	if chain != "" || key != "" {
		if chain == "" || key == "" {
			return devverifier.Identity{}, fmt.Errorf("%s and %s must be set together", envChainFile, envKeyFile)
		}
		return devverifier.LoadIdentity(chain, key)
	}
	return devverifier.NewIdentity(envOr(envDNSName, defaultDNSName))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// writeChain mints an identity and writes chain, key and root PEM files to dir.
func writeChain(dir, dnsName string) error {
	id, err := devverifier.NewIdentity(dnsName)
	if err != nil {
		return err
	}
	key, err := id.KeyPEM()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range map[string][]byte{chainFileName: id.ChainPEM(), rootFileName: id.RootPEM()} {
		if err := os.WriteFile(filepath.Join(dir, name), content, certFileMode); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, keyFileName), key, keyFileMode); err != nil {
		return err
	}
	fmt.Printf("wrote %s identity to %s\n", id.ClientID(), dir)
	return nil
}
