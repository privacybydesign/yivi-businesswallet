//go:build wsca

package eudiholder

import (
	"context"
	"crypto"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/privacybydesign/irmago/eudi/holderkeys"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
	"github.com/privacybydesign/irmago/eudi/services"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
	"github.com/privacybydesign/irmago/eudi/storage/db"
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
	return holderkeys.NewSignerKeyBinder(&wscaRowSigner{Signer: signer, keys: db.NewHolderBindingKeyStore(st.Db())}), nil
}

// wscaRefPrefix marks the WSCA key id the issuance binder records in the
// holder-key row's private-key column (irmabinding.bindingKeyModel): the key
// itself never leaves the HSM, the row remembers which one backs the credential.
const wscaRefPrefix = "wsca:"

// wscaRowSigner is the org's WSCA signer with key resolution through irmago's
// holder-key rows. irmabinding.Signer.Reference matches the credential's cnf key
// against the WSCA's key list by thumbprint, but that list carries the raw EC
// point in public_key_hex (the DER form is public_key_der_hex), so the match
// never succeeds. The issuance binder wrote the WSCA key id into the row it
// stored under the key's DID URL or thumbprint — the same lookup irmago's
// software binder uses — so the reference is read from there, and the WSCA list
// is only the fallback for a row this backend did not write.
type wscaRowSigner struct {
	*irmabinding.Signer
	keys db.HolderBindingKeyStore
}

func (s *wscaRowSigner) Reference(pub jwk.Key) (string, error) {
	if kid, ok := pub.KeyID(); ok && kid != "" {
		if ref, ok := s.refByDID(kid); ok {
			return ref, nil
		}
		if base := strings.SplitN(kid, "#", 2)[0]; base != kid {
			if ref, ok := s.refByDID(base); ok {
				return ref, nil
			}
		}
	}
	if thumb, err := pub.Thumbprint(crypto.SHA256); err == nil {
		if row, err := s.keys.GetByThumbprint(hex.EncodeToString(thumb)); err == nil {
			if ref, ok := wscaRef(row.PrivateKey); ok {
				return ref, nil
			}
		}
	}
	return s.Signer.Reference(pub)
}

func (s *wscaRowSigner) refByDID(did string) (string, bool) {
	row, err := s.keys.GetByDidUrl(did)
	if err != nil {
		return "", false
	}
	return wscaRef(row.PrivateKey)
}

func wscaRef(privateKey []byte) (string, bool) {
	ref, ok := strings.CutPrefix(string(privateKey), wscaRefPrefix)
	return ref, ok && ref != ""
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
