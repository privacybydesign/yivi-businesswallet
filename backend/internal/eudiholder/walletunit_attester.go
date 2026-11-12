package eudiholder

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
)

// es256SigBytes is the size of a raw r||s P-256 signature.
const es256SigBytes = 64

// WalletUnitAttester issues the wallet unit attestations (WE BUILD CS-05) an
// organization's wallet presents when it receives a credential: walletunit.Provider.
type WalletUnitAttester interface {
	IssueBWIA(ctx context.Context, orgID uuid.UUID, instanceKey *ecdsa.PublicKey) (string, error)
	IssueSKA(ctx context.Context, orgID uuid.UUID, keys []*ecdsa.PublicKey) (string, error)
}

// ErrNotAttestable is what a WalletUnitAttester wraps when the org's wallet unit
// cannot be attested right now: the redemption then goes without an attestation,
// so issuers that do not require one still deliver.
var ErrNotAttestable = errors.New("eudiholder: wallet unit not attestable")

// SetWalletUnitAttester makes every redemption present the org's wallet unit
// attestation. Set once at boot before serving.
func (e *Engine) SetWalletUnitAttester(a WalletUnitAttester) { e.walletUnit = a }

// orgWalletAttester is irmago's WalletAttester for one redemption of one org.
type orgWalletAttester struct {
	orgID uuid.UUID
	// keysInHSM is set when holder keys live in the WSCA/HSM; only then can
	// they be attested.
	keysInHSM bool
	issueBWIA func(instanceKey *ecdsa.PublicKey) (string, error)
	issueSKA  func(keys []*ecdsa.PublicKey) (string, error)
	warn      func(msg string, args ...any)
}

// newOrgWalletAttester binds the redemption's context into the calls, since
// irmago's WalletAttester methods take none.
func newOrgWalletAttester(ctx context.Context, orgID uuid.UUID, provider WalletUnitAttester, keysInHSM bool) *orgWalletAttester {
	return &orgWalletAttester{
		orgID:     orgID,
		keysInHSM: keysInHSM,
		issueBWIA: func(instanceKey *ecdsa.PublicKey) (string, error) {
			return provider.IssueBWIA(ctx, orgID, instanceKey)
		},
		issueSKA: func(keys []*ecdsa.PublicKey) (string, error) {
			return provider.IssueSKA(ctx, orgID, keys)
		},
		warn: func(msg string, args ...any) { slog.WarnContext(ctx, msg, args...) },
	}
}

var _ openid4vci.WalletAttester = (*orgWalletAttester)(nil)

// ClientAttestation attests a fresh instance key, used for this session only.
func (a *orgWalletAttester) ClientAttestation(credentialIssuer string) (*openid4vci.ClientAttestation, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: instance key: %w", err)
	}
	bwia, err := a.issueBWIA(&key.PublicKey)
	if errors.Is(err, ErrNotAttestable) {
		a.warn("eudiholder: receiving without a wallet unit attestation",
			slog.String("org_id", a.orgID.String()), slog.String("issuer", credentialIssuer), slog.String("reason", err.Error()))
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("eudiholder: wallet unit attestation: %w", err)
	}
	sub, err := jwtSubject(bwia)
	if err != nil {
		return nil, err
	}
	return &openid4vci.ClientAttestation{JWT: bwia, ClientID: sub, Key: &key.PublicKey, Sign: es256Signer(key)}, nil
}

// KeyAttestation attests a holder key, only when it is HSM-held: an SKA for a
// software key would be a false claim.
func (a *orgWalletAttester) KeyAttestation(_ string, key *ecdsa.PublicKey, _ *string) (string, error) {
	if !a.keysInHSM {
		return "", nil
	}
	return a.issueSKA([]*ecdsa.PublicKey{key})
}

func es256Signer(key *ecdsa.PrivateKey) func([]byte) ([]byte, error) {
	return func(input []byte) ([]byte, error) {
		digest := sha256.Sum256(input)
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			return nil, err
		}
		sig := make([]byte, es256SigBytes)
		r.FillBytes(sig[:es256SigBytes/2])
		s.FillBytes(sig[es256SigBytes/2:])
		return sig, nil
	}
}

// jwtSubject reads `sub` from a JWT this process just signed.
func jwtSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("eudiholder: wallet unit attestation is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("eudiholder: wallet unit attestation payload: %w", err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Sub == "" {
		return "", fmt.Errorf("eudiholder: wallet unit attestation has no sub: %v", err)
	}
	return claims.Sub, nil
}
