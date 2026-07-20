//go:build wsca

package main

import (
	"secdsa/mobile/walletmobile"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wscawallet"
)

// wscaCompiledIn reports whether the WSCA client (walletmobile, a private module)
// is linked into this binary. True only under the `wsca` build tag.
const wscaCompiledIn = true

// newWSCAWalletClient opens the org's per-org walletmobile wallet (the real WSCA
// client). Built only under the `wsca` tag; the default build uses the stub in
// wsca_off.go.
func newWSCAWalletClient(cfg config.Config, orgID uuid.UUID) (wscawallet.WalletClient, error) {
	return walletmobile.NewWallet(
		cfg.AttestationHolderWSCAURL,
		eudiholder.OrgKeystoreDir(cfg.AttestationHolderWSCAKeystoreDir, orgID),
		cfg.AttestationHolderWSCAInsecure,
	)
}
