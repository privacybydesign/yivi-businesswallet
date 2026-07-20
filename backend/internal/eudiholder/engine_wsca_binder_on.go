//go:build wsca

package eudiholder

import (
	"context"
	"fmt"
	"secdsa/mobile/walletmobile"
	"secdsa/mobile/walletmobile/irmabinding"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/services"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
)

// holderKeyBinder (wsca build) builds the org's WSCA issuance key binder over its
// already-activated walletmobile wallet, or irmago's default storage-backed
// software binder when WSCA is not configured.
func (e *Engine) holderKeyBinder(ctx context.Context, orgID uuid.UUID, st irmastorage.Storage) (openid4vci.HolderKeyBinder, error) {
	if e.wsca == nil {
		return services.NewHolderBindingKeyService(st.Db()), nil
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
