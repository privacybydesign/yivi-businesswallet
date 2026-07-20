//go:build !wsca

package main

import (
	"errors"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wscawallet"
)

// wscaCompiledIn is false in the default build: the WSCA client (walletmobile, a
// private module) is not linked in, so the binary needs no access to that repo.
const wscaCompiledIn = false

// newWSCAWalletClient (default build) has no WSCA client. Activation attempts
// error; newAttestationHolder fails at boot if a WSCA URL is configured.
func newWSCAWalletClient(config.Config, uuid.UUID) (wscawallet.WalletClient, error) {
	return nil, errors.New("cmd/api: WSCA is not compiled in (build with -tags wsca)")
}
