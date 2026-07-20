package eudiholder

import (
	"context"
	"encoding/hex"
	"path/filepath"

	"github.com/google/uuid"
)

// WSCAConfig enables WSCA-backed holder binding on the redeem path: the SD-JWT VC
// holder binding keys are generated and their OpenID4VCI proofs signed by the
// wallet-provider WSCA/HSM (irmabinding over walletmobile) instead of software
// keys, so the holder private key never enters this process. Optional; when the
// engine has no WSCAConfig the default software binder is used (backwards
// compatible). See .ai/features/wsca-holder-binding.md.
//
// The WSCA client (walletmobile) is a private module, so the code that actually
// talks to it lives behind the `wsca` build tag (engine_wsca_binder_on.go). A
// default build (no tag) has no WSCA client: holderKeyBinder then errors if a
// WSCAConfig was set, so a misconfigured binary fails loudly instead of silently
// downgrading to software keys.
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

// OrgKeystoreDir is the per-org walletmobile keystore directory under base. It is
// the single source of truth for the layout: the activation flow
// (internal/wscawallet) and this redeem path MUST agree, or activation writes a
// keystore the redeem path never finds.
func OrgKeystoreDir(base string, orgID uuid.UUID) string {
	return filepath.Join(base, hex.EncodeToString(orgID[:]))
}
