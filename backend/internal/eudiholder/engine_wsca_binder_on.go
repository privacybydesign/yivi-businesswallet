//go:build wsca

package eudiholder

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/holderkeys"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
	"github.com/privacybydesign/irmago/eudi/services"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
	"github.com/privacybydesign/wallet-provider/mobile/walletmobile"
	"github.com/privacybydesign/wallet-provider/mobile/walletmobile/irmabinding"
)

// holderKeyBinder (wsca build) builds the org's WSCA issuance key binder over its
// already-activated walletmobile wallet, or irmago's default storage-backed
// software binder when WSCA is not configured.
func (e *Engine) holderKeyBinder(ctx context.Context, orgID uuid.UUID, st irmastorage.Storage) (openid4vci.HolderKeyBinder, error) {
	if e.wsca == nil {
		return services.NewHolderBindingKeyService(st.Db()), nil
	}
	signer, err := e.wscaSigner(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return irmabinding.NewIssuanceBinderFactory(signer)(st), nil
}

// presentationKeyBinder (wsca build) signs the key-binding JWT of an OpenID4VP
// presentation: through the org's WSCA signer when configured — the same
// signer that generated the holder key at issuance, so cnf and KB-JWT agree —
// else through irmago's storage-backed software binder over the same key rows
// holderKeyBinder wrote.
func (e *Engine) presentationKeyBinder(ctx context.Context, orgID uuid.UUID, st irmastorage.Storage) (sdjwt.KeyBinder, error) {
	if e.wsca == nil {
		return sdjwt.NewDefaultKeyBinder(services.NewHolderBindingKeyService(st.Db())), nil
	}
	signer, err := e.wscaSigner(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return holderkeys.NewSignerKeyBinder(signer), nil
}

// wscaSigner opens the org's already-activated walletmobile wallet and wraps it
// in irmago's HolderSigner shape with the org's sealed activation secret.
func (e *Engine) wscaSigner(ctx context.Context, orgID uuid.UUID) (*irmabinding.Signer, error) {
	w, err := walletmobile.NewWallet(e.wsca.BaseURL, OrgKeystoreDir(e.wsca.KeystoreDir, orgID), e.wsca.Insecure)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: open WSCA wallet org %s: %w", orgID, err)
	}
	secret, err := e.wsca.Secret(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: WSCA secret org %s: %w", orgID, err)
	}
	return irmabinding.NewSigner(w, func() (string, error) { return secret, nil }), nil
}
