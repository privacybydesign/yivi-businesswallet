package eudiholder

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"secdsa/mobile/walletmobile"
	"secdsa/mobile/walletmobile/irmabinding"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
)

// WSCAConfig enables WSCA-backed holder binding on the redeem path: the SD-JWT VC
// holder binding keys are generated and their OpenID4VCI proofs signed by the
// wallet-provider WSCA/HSM (irmabinding over walletmobile) instead of software
// keys, so the holder private key never enters this process. Optional; when the
// engine has no WSCAConfig the default software binder is used (backwards
// compatible). See .ai/features/wsca-holder-binding.md.
//
// Each org's walletmobile keystore (its possession key U) must already be
// activated (walletmobile.Activate) before a redeem — that is the org-admin setup
// flow, separate from this receive path.
type WSCAConfig struct {
	// BaseURL is the wallet-provider (WSCA) base URL.
	BaseURL string
	// KeystoreDir is the parent directory under which each org's walletmobile
	// keystore lives (KeystoreDir/<orghex>). On a server deployment this is a
	// persistent volume. (Hardening: back U with a server HSM via
	// walletmobile.NewWalletWithHardwareSigner instead of the JKS keystore.)
	KeystoreDir string
	// Insecure trusts the wallet-provider's dev TLS cert (local/staging only).
	Insecure bool
	// Secret returns the org's WSCA activation secret, decrypted from the sealed
	// store (internal/wsca). It is the SECDSA knowledge factor; never log it.
	Secret func(ctx context.Context, orgID uuid.UUID) (string, error)
}

// SetWSCA enables WSCA-backed holder binding. nil (the default) keeps software
// keys. Set once at boot before serving.
func (e *Engine) SetWSCA(cfg *WSCAConfig) { e.wsca = cfg }

// holderKeyBinder builds the org's WSCA issuance key binder over its
// already-activated walletmobile wallet, or returns nil to fall back to the
// default software binder (when WSCA is not configured).
func (e *Engine) holderKeyBinder(ctx context.Context, orgID uuid.UUID, st irmastorage.Storage) (openid4vci.HolderKeyBinder, error) {
	if e.wsca == nil {
		return nil, nil
	}
	w, err := walletmobile.NewWallet(e.wsca.BaseURL, OrgKeystoreDir(e.wsca.KeystoreDir, orgID), e.wsca.Insecure)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: open WSCA wallet org %s: %w", orgID, err)
	}
	secret, err := e.wsca.Secret(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: WSCA secret org %s: %w", orgID, err)
	}
	signer := irmabinding.NewSigner(w, func() (string, error) { return secret, nil })
	return irmabinding.NewIssuanceBinderFactory(signer)(st), nil
}

// OrgKeystoreDir is the per-org walletmobile keystore directory under base. It is
// the single source of truth for the layout: the activation flow
// (internal/wscawallet) and this redeem path MUST agree, or activation writes a
// keystore the redeem path never finds.
func OrgKeystoreDir(base string, orgID uuid.UUID) string {
	return filepath.Join(base, hex.EncodeToString(orgID[:]))
}
