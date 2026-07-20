//go:build !wsca

package eudiholder

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/services"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
)

// errWSCANotCompiled reports a WSCAConfig set on a binary built without the
// `wsca` tag (so the walletmobile client is not linked in). Fail loudly rather
// than silently signing holder-binding proofs with software keys.
var errWSCANotCompiled = errors.New("eudiholder: WSCA configured but binary built without -tags wsca")

// holderKeyBinder (default build) has no WSCA client linked in. With no
// WSCAConfig it returns irmago's default storage-backed software binder. If a
// WSCAConfig was set it errors, so a misconfigured deployment fails instead of
// downgrading.
func (e *Engine) holderKeyBinder(_ context.Context, _ uuid.UUID, st irmastorage.Storage) (openid4vci.HolderKeyBinder, error) {
	if e.wsca != nil {
		return nil, errWSCANotCompiled
	}
	return services.NewHolderBindingKeyService(st.Db()), nil
}
