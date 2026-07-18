package eudiholder

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"
	"github.com/privacybydesign/irmago/eudi"
	"github.com/privacybydesign/irmago/eudi/credentials/sdjwtvc"
	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/utils"
)

// redirectURI is unused by the pre-authorized-code grant (no browser redirect),
// but NewSession requires a value.
const redirectURI = "ybw-holder://redirect"

// Redeem runs irmago's OpenID4VCI holder flow for offerURI against the sending
// org's issuer, verifying and storing the received credential into this org's
// per-org holder storage (the same storage.Storage the engine caches). The flow
// is auto-consented: over the authenticated QERDS channel there is no operator to
// prompt, and no tx_code (org offers are minted without one — see
// .ai/features/oid4vci-over-qerds.md §4). Only the pre-authorized-code grant is
// accepted; an authorization-code offer is declined (it needs interactive auth).
func (e *Engine) Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (Redeemed, error) {
	st, err := e.engineFor(ctx, orgID)
	if err != nil {
		return Redeemed{}, err
	}

	conf, err := eudi.NewConfiguration(st)
	if err != nil {
		return Redeemed{}, fmt.Errorf("eudiholder: redeem config org %s: %w", orgID, err)
	}
	if e.redeem.StagingTrustAnchors {
		conf.EnableStagingTrustAnchors()
	}
	if err := conf.Reload(); err != nil {
		return Redeemed{}, fmt.Errorf("eudiholder: redeem load trust anchors org %s: %w", orgID, err)
	}

	verCtx, err := e.verificationContext(conf)
	if err != nil {
		return Redeemed{}, err
	}
	var opts []openid4vci.ClientOption
	binder, err := e.holderKeyBinder(ctx, orgID, st)
	if err != nil {
		return Redeemed{}, err
	}
	if binder != nil {
		// WSCA-backed: the holder binding key + its proof of possession are
		// produced by the wallet-provider HSM, not software keys.
		opts = append(opts, openid4vci.WithHolderKeyBinder(binder))
	}
	client, err := openid4vci.NewClient(e.httpClient, conf, sdjwtvc.NewHolderVerificationProcessor(verCtx), opts...)
	if err != nil {
		return Redeemed{}, fmt.Errorf("eudiholder: redeem client org %s: %w", orgID, err)
	}
	if e.redeem.AllowInsecureHTTP {
		client.AllowInsecureHttpForTesting()
	}

	handler := newRedeemHandler()
	sessionID := int(e.sessionCounter.Add(1))
	client.NewSession(sessionID, offerURI, redirectURI, handler)

	select {
	case <-ctx.Done():
		return Redeemed{}, ctx.Err()
	case res := <-handler.done:
		if res.err != nil {
			return Redeemed{}, fmt.Errorf("eudiholder: redeem org %s: %w", orgID, res.err)
		}
		return res.redeemed, nil
	}
}

// verificationContext builds the SD-JWT VC trust context: a configured
// trusted-issuer CA chain when set (the holder analogue of EUDI_ISSUER_CHAIN),
// otherwise irmago's built-in trust model loaded into conf.Issuers.
func (e *Engine) verificationContext(conf *eudi.Configuration) (sdjwtvc.SdJwtVcVerificationContext, error) {
	if len(e.redeem.TrustChainPEM) > 0 {
		opts, err := utils.CreateX509VerifyOptionsFromCertChain(e.redeem.TrustChainPEM)
		if err != nil {
			return sdjwtvc.SdJwtVcVerificationContext{}, fmt.Errorf("eudiholder: parse holder trust chain: %w", err)
		}
		return sdjwtvc.SdJwtVcVerificationContext{
			X509VerificationContext: &eudijwt.StaticVerificationContext{VerifyOpts: *opts},
			Clock:                   eudijwt.NewSystemClock(),
			JwtVerifier:             sdjwtvc.NewJwxJwtVerifier(),
		}, nil
	}
	return sdjwtvc.SdJwtVcVerificationContext{
		X509VerificationContext: &conf.Issuers,
		Clock:                   eudijwt.NewSystemClock(),
		JwtVerifier:             sdjwtvc.NewJwxJwtVerifier(),
	}, nil
}

// redeemResult carries the outcome of the asynchronous, callback-driven session
// back to Redeem over a buffered channel.
type redeemResult struct {
	redeemed Redeemed
	err      error
}

// redeemHandler bridges irmago's callback-based openid4vci.Handler to a single
// synchronous result. It auto-grants the pre-authorized-code flow (no tx_code)
// and the add-to-wallet permission, and declines the authorization-code flow.
type redeemHandler struct {
	done chan redeemResult
}

func newRedeemHandler() *redeemHandler {
	return &redeemHandler{done: make(chan redeemResult, 1)}
}

func (h *redeemHandler) Success(_ string, issued []*clientmodels.Credential) {
	if len(issued) == 0 {
		h.done <- redeemResult{err: errors.New("issuer returned no credential")}
		return
	}
	c := issued[0]
	h.done <- redeemResult{redeemed: Redeemed{
		Ref:    credentialInstanceRef(c),
		VCT:    c.CredentialId,
		Issuer: c.Issuer.Id,
	}}
}

func (h *redeemHandler) Cancelled() {
	h.done <- redeemResult{err: errors.New("session cancelled")}
}

func (h *redeemHandler) Failure(err *clientmodels.SessionError) {
	h.done <- redeemResult{err: fmt.Errorf("session error %q: %s", err.ErrorType, err.Info)}
}

func (h *redeemHandler) RequestPreAuthorizedCodeFlowPermission(
	_ *clientmodels.PreAuthorizedCodeFlowPermissionRequest,
	_ *clientmodels.TrustedParty,
	callback openid4vci.TokenPermissionHandler,
) {
	callback(true, nil)
}

func (h *redeemHandler) RequestAuthorizationCodeFlowPermission(
	_ *clientmodels.AuthorizationCodeFlowRequest,
	_ *clientmodels.TrustedParty,
	callback openid4vci.AuthCodeHandler,
) {
	// Authorization-code issuance needs interactive holder authentication, which a
	// headless business wallet cannot perform on receive; decline it.
	callback(false, nil)
}

func (h *redeemHandler) RequestPermission(
	_ []*clientmodels.Credential,
	_ *clientmodels.TrustedParty,
	callback openid4vci.PermissionHandler,
) {
	callback(true)
}

// credentialInstanceRef returns the stored instance id for the credential (there
// is one per format; the receive flow stores a single SD-JWT VC).
func credentialInstanceRef(c *clientmodels.Credential) string {
	for _, id := range c.CredentialInstanceIds {
		return id
	}
	return ""
}
