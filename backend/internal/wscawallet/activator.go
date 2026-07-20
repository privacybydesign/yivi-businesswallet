// Package wscawallet activates and rotates an organization's WSCA (wallet-
// provider) holder wallet: it drives the SECDSA activation over walletmobile and
// seals the activation secret via internal/wsca. The org-admin setup/rotation
// flows call it (the human-in-the-loop moments — see
// .ai/features/wsca-holder-binding.md); the eudiholder redeem path then uses the
// sealed secret to sign holder-binding proofs autonomously.
package wscawallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

// ErrAlreadyActivated is returned by Activate when the org already has an
// activated WSCA wallet (use Rotate to change the secret).
var ErrAlreadyActivated = errors.New("wscawallet: organization wallet already activated")

// WalletClient is the subset of secdsa/mobile/walletmobile.Wallet the activator
// uses. Accept an interface so the activation logic is unit-testable without a
// live WSCA.
type WalletClient interface {
	IsActivated() bool
	Activate(pin string) error
	ChangePIN(currentPIN, newPIN string) error
	CertificateID() string
}

// Activator activates and rotates per-org WSCA wallets and seals their secrets.
type Activator struct {
	// enabled is true when a wallet-provider URL is configured (WSCA opt-in).
	enabled bool
	// newWallet opens the org's per-org walletmobile wallet (keystore dir); wired
	// at boot to walletmobile.NewWallet and swappable in tests.
	newWallet func(orgID uuid.UUID) (WalletClient, error)
	store     *wsca.Store
}

func NewActivator(enabled bool, newWallet func(orgID uuid.UUID) (WalletClient, error), store *wsca.Store) *Activator {
	return &Activator{enabled: enabled, newWallet: newWallet, store: store}
}

// Configured reports whether activation is possible on this deployment: WSCA is
// enabled (a wallet-provider URL is set) and the sealed-secret store has a KEK.
func (a *Activator) Configured() bool { return a.enabled && a.store.Configured() }

// Activate runs the one-time SECDSA activation for the org with secret (the
// knowledge factor an admin sets) and seals it. secret must satisfy the WSCA PIN
// policy (>=5 ASCII digits); walletmobile enforces it.
func (a *Activator) Activate(ctx context.Context, orgID uuid.UUID, secret string) (wsca.Account, error) {
	if !a.Configured() {
		return wsca.Account{}, wsca.ErrNotConfigured
	}
	w, err := a.newWallet(orgID)
	if err != nil {
		return wsca.Account{}, fmt.Errorf("wscawallet: open wallet org %s: %w", orgID, err)
	}
	if w.IsActivated() {
		return wsca.Account{}, ErrAlreadyActivated
	}
	if err := w.Activate(secret); err != nil {
		return wsca.Account{}, fmt.Errorf("wscawallet: activate org %s: %w", orgID, err)
	}
	// walletmobile exposes only the certificate id, so the first certificate is
	// the org's stable WSCA account reference (rotation keeps it; see wsca.Store).
	certID := w.CertificateID()
	return a.store.Activate(ctx, orgID, secret, certID, certID)
}

// Rotate proves currentSecret and re-activates with newSecret (SECDSA ChangePIN
// keeps the possession key U and issues a fresh certificate), then reseals.
func (a *Activator) Rotate(ctx context.Context, orgID uuid.UUID, currentSecret, newSecret string) (wsca.Account, error) {
	if !a.Configured() {
		return wsca.Account{}, wsca.ErrNotConfigured
	}
	w, err := a.newWallet(orgID)
	if err != nil {
		return wsca.Account{}, fmt.Errorf("wscawallet: open wallet org %s: %w", orgID, err)
	}
	if !w.IsActivated() {
		return wsca.Account{}, wsca.ErrNotActivated
	}
	if err := w.ChangePIN(currentSecret, newSecret); err != nil {
		return wsca.Account{}, fmt.Errorf("wscawallet: rotate org %s: %w", orgID, err)
	}
	return a.store.Rotate(ctx, orgID, newSecret, w.CertificateID())
}

// Status returns the org's WSCA account view (wsca.ErrNotActivated when absent).
func (a *Activator) Status(ctx context.Context, orgID uuid.UUID) (wsca.Account, error) {
	return a.store.Get(ctx, orgID)
}
