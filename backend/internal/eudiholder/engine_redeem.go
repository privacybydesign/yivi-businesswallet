package eudiholder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"
	"github.com/privacybydesign/irmago/eudi"
	"github.com/privacybydesign/irmago/eudi/credentials/sdjwtvc"
	"github.com/privacybydesign/irmago/eudi/credentials/statuslist"
	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
	"github.com/privacybydesign/irmago/eudi/services"
	"github.com/privacybydesign/irmago/eudi/storage/db"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"github.com/privacybydesign/irmago/eudi/utils"
)

// redirectURI is unused by the pre-authorized-code grant (no browser redirect),
// but NewSession requires a value.
const redirectURI = "ybw-holder://redirect"

// defaultHolderLocale is the locale irmago's holder credential services resolve
// display metadata in during receive. Receive is headless, and this engine
// re-resolves display per request at read time (Claims/Displays take the
// caller's language), so this only affects irmago's internal storage-time
// resolution.
const defaultHolderLocale = "en"

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

	// statusChecker performs the IETF Token Status List check the newer irmago
	// holder pipeline supports. It is shared between the receive-time verification
	// context (which rejects a credential whose status-list bit is not valid) and
	// the credential service's revocation view, exactly as irmago's own wallet
	// wires it. Its cache is the org's own status_list_cache table (AutoMigrated
	// with the rest of the holder schema). Credentials that carry no
	// status.status_list reference are unaffected — the check is a no-op for them.
	statusChecker := statuslist.NewChecker(statuslist.VerificationContext{
		X509Context: &conf.Issuers,
		Clock:       eudijwt.NewSystemClock(),
	}, db.NewStatusListCacheStore(st.Db()))

	verCtx, err := e.verificationContext(conf, statusChecker)
	if err != nil {
		return Redeemed{}, err
	}
	// holderKeyBinder is required by NewClient (non-nil): the WSCA-backed binder
	// when configured (holder key + proof of possession produced by the
	// wallet-provider HSM, not software keys), otherwise irmago's default
	// storage-backed software binder.
	binder, err := e.holderKeyBinder(ctx, orgID, st)
	if err != nil {
		return Redeemed{}, err
	}
	// currentLocale is shared by the credential service and the client (matching
	// irmago's wallet assembly); receive is headless, so the default locale stands.
	currentLocale := clientmodels.NewCurrentLocale(defaultHolderLocale)
	// credentialService is the holder-side store irmago's OpenID4VCI client now
	// requires to verify and persist the received credential; its revocation
	// service is built on the shared status checker. The store is wrapped so this
	// redemption learns the instance id of the batch it actually persisted — see
	// storeRecorder.
	credStore := &storeRecorder{CredentialStore: db.NewCredentialStore(st.Db())}
	credentialService := services.NewCredentialService(
		credStore,
		db.NewHolderBindingKeyStore(st.Db()),
		st.FileSystem(),
		services.NewRevocationService(statusChecker, credStore),
		currentLocale,
	)
	client, err := openid4vci.NewClient(
		e.httpClient,
		conf,
		sdjwtvc.NewHolderVerificationProcessor(verCtx),
		credentialService,
		binder,
		currentLocale,
	)
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
		redeemed := res.redeemed
		// irmago hands back the *offered* credential (buildOfferedCredentials), which
		// populates neither CredentialInstanceIds nor Hash — so credentialInstanceRef
		// yields "" on every real redemption and the handed-back credential carries no
		// way to name the row just written. Take the ref from the batch this
		// redemption stored instead, so the held index gets a precise per-credential
		// ref for claims/delete even when the org already holds another credential of
		// the same vct.
		if redeemed.Ref == "" {
			redeemed.Ref = credStore.refFor(redeemed.VCT)
		}
		if redeemed.Ref == "" {
			// Without a ref the held row can only be resolved by vct, which is
			// ambiguous once the org holds a second credential of the type.
			slog.WarnContext(ctx, "eudiholder: redeemed credential stored without an instance ref",
				slog.String("orgId", orgID.String()), slog.String("vct", redeemed.VCT))
		}
		return redeemed, nil
	}
}

// verificationContext builds the SD-JWT VC trust context: a configured
// trusted-issuer CA chain when set (the holder analogue of EUDI_ISSUER_CHAIN),
// otherwise irmago's built-in trust model loaded into conf.Issuers. statusChecker
// is set so a received credential that references a Token Status List is rejected
// unless its bit reads valid (a no-op for credentials that carry no reference).
func (e *Engine) verificationContext(conf *eudi.Configuration, statusChecker *statuslist.Checker) (sdjwtvc.SdJwtVcVerificationContext, error) {
	if len(e.redeem.TrustChainPEM) > 0 {
		opts, err := utils.CreateX509VerifyOptionsFromCertChain(e.redeem.TrustChainPEM)
		if err != nil {
			return sdjwtvc.SdJwtVcVerificationContext{}, fmt.Errorf("eudiholder: parse holder trust chain: %w", err)
		}
		return sdjwtvc.SdJwtVcVerificationContext{
			X509VerificationContext: &eudijwt.StaticVerificationContext{VerifyOpts: *opts},
			Clock:                   eudijwt.NewSystemClock(),
			JwtVerifier:             sdjwt.NewJwxJwtVerifier(),
			StatusChecker:           statusChecker,
		}, nil
	}
	return sdjwtvc.SdJwtVcVerificationContext{
		X509VerificationContext: &conf.Issuers,
		Clock:                   eudijwt.NewSystemClock(),
		JwtVerifier:             sdjwt.NewJwxJwtVerifier(),
		StatusChecker:           statusChecker,
	}, nil
}

// storeRecorder decorates irmago's holder credential store to capture the batches
// a redemption persists. irmago reports a completed session through
// Handler.Success with the credential built by session.buildOfferedCredentials,
// which sets neither CredentialInstanceIds nor Hash — so the credential handed back
// cannot identify the row that was just written, and vct alone cannot either once
// the org holds two credentials of one type. Recording at the insert closes that
// gap exactly: StoreBatch is where the ids are generated, and GORM writes them into
// the struct it inserted.
type storeRecorder struct {
	db.CredentialStore

	// mu guards batches: irmago stores one batch per credential configuration, and
	// a caller may cancel while a store is in flight.
	mu      sync.Mutex
	batches []storedBatch
}

// storedBatch is one persisted credential batch: its credential type and the id of
// its first instance, which is the ref the held index records (the receive flow
// indexes one held row per received credential, matching Store's single-instance
// batches).
type storedBatch struct {
	vct        string
	instanceID string
}

func (r *storeRecorder) StoreBatch(batch *models.CredentialBatch) error {
	if err := r.CredentialStore.StoreBatch(batch); err != nil {
		return err
	}
	if len(batch.Instances) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, storedBatch{
		vct:        batch.VerifiableCredentialType,
		instanceID: batch.Instances[0].ID.String(),
	})
	return nil
}

// refFor returns the instance id of the batch this redemption stored for vct.
// It prefers an exact vct match and otherwise falls back to the first batch stored:
// the credential Success reports is built from the first fetched configuration,
// which storeCredentials persists first, so that is the same credential even when
// the issued vct differs from the one the caller passed. Empty when the session
// stored nothing.
func (r *storeRecorder) refFor(vct string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.batches {
		if b.vct == vct {
			return b.instanceID
		}
	}
	if len(r.batches) > 0 {
		return r.batches[0].instanceID
	}
	return ""
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
	h.done <- redeemResult{
		redeemed: Redeemed{
			Ref:    credentialInstanceRef(c),
			VCT:    c.CredentialId,
			Issuer: c.Issuer.Id,
		},
	}
}

func (h *redeemHandler) Cancelled() {
	h.done <- redeemResult{err: errors.New("session cancelled")}
}

func (h *redeemHandler) Failure(err *clientmodels.SessionError) {
	// Surface every populated field: irmago sets only WrappedError on many
	// failure paths (leaving ErrorType/Info empty), and RemoteStatus/RemoteError
	// carry the issuer's HTTP response — the earlier message dropped all of them.
	parts := make([]string, 0, 5)
	if err.ErrorType != "" {
		parts = append(parts, "type="+err.ErrorType)
	}
	if err.WrappedError != "" {
		parts = append(parts, "wrapped="+err.WrappedError)
	}
	if err.Info != "" {
		parts = append(parts, "info="+err.Info)
	}
	if err.RemoteStatus != 0 {
		parts = append(parts, fmt.Sprintf("remoteStatus=%d", err.RemoteStatus))
	}
	if err.RemoteError != nil {
		parts = append(parts, fmt.Sprintf("remoteError=%+v", err.RemoteError))
	}
	detail := strings.Join(parts, " ")
	if detail == "" {
		detail = "(no detail)"
	}
	h.done <- redeemResult{err: fmt.Errorf("openid4vci session failed: %s", detail)}
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
