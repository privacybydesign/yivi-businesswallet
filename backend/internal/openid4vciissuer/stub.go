package openid4vciissuer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const stubIssuanceIDBytes = 16

// The stub offer link: its prefix, and the query parameters that carry the
// credential type and its claims to the stub holder.
const (
	stubOfferPrefix = "openid-credential-offer://stub?"
	stubParamVCT    = "vct"
	stubParamName   = "name"
	stubParamIssuer = "issuer"
	stubParamClaims = "claims"
)

// StubOffer is what a stub offer link carries to the stub holder.
type StubOffer struct {
	VCT         string
	DisplayName string
	IssuerName  string
	Claims      map[string]any
}

// StubIssuer is an in-process issuer for dev / CI (ATTESTATION_ISSUER=stub). It
// returns a deterministic-shaped offer and reports the credential as issued
// immediately, so the whole offer -> claim loop runs offline. It is the issuer
// equivalent of the QERDS StubProvider and the faked verifier. Selected by
// config, never code.
type StubIssuer struct {
	// claimDelay holds an offer at PENDING for that long after it was made, so
	// a screen recording can show its QR before the poll reports it claimed.
	// Zero (NewStubIssuer) reports every offer issued at once.
	claimDelay time.Duration
	created    *sync.Map // issuance id -> time.Time; nil without a delay
}

func NewStubIssuer() StubIssuer { return StubIssuer{} }

// NewStubIssuerWithClaimDelay is NewStubIssuer that keeps each offer pending
// for delay before reporting it issued (ATTESTATION_STUB_CLAIM_DELAY).
func NewStubIssuerWithClaimDelay(delay time.Duration) StubIssuer {
	if delay <= 0 {
		return NewStubIssuer()
	}
	return StubIssuer{claimDelay: delay, created: &sync.Map{}}
}

func (s StubIssuer) CreateOffer(_ context.Context, req OfferRequest) (Offer, error) {
	id, err := randomID()
	if err != nil {
		return Offer{}, err
	}
	if s.created != nil {
		s.created.Store(id, time.Now())
	}
	q := url.Values{}
	q.Set("credential", req.CredentialConfigID)
	q.Set("id", id)
	// The type and claims ride along so the stub holder can hold what was
	// issued rather than a placeholder. Dev / CI only: a real offer is a
	// pre-authorized code, and the claims stay at the issuer.
	if req.VCT != "" {
		q.Set(stubParamVCT, req.VCT)
	}
	if req.DisplayName != "" {
		q.Set(stubParamName, req.DisplayName)
	}
	if req.IssuerName != "" {
		q.Set(stubParamIssuer, req.IssuerName)
	}
	if len(req.Claims) > 0 {
		claims, err := json.Marshal(req.Claims)
		if err != nil {
			return Offer{}, fmt.Errorf("openid4vciissuer: stub claims: %w", err)
		}
		q.Set(stubParamClaims, base64.RawURLEncoding.EncodeToString(claims))
	}
	offer := Offer{
		IssuanceID: id,
		OfferURI:   stubOfferPrefix + q.Encode(),
	}
	if req.UseTxCode {
		offer.TxCode = "000000"
	}
	return offer, nil
}

// Status reports the credential as issued: the stub has no real wallet on the
// other end, so a poll resolves the offer for the demo/tests — at once, or once
// the claim delay has passed. An offer it has no creation time for (made before
// a restart) is issued. The credential uuid is derived from the issuance id so
// the revoke path has a stable handle to key on. The instance is ignored (the
// stub has no per-instance routing).
func (s StubIssuer) Status(_ context.Context, _, issuanceID string) (IssuanceStatus, error) {
	if s.created != nil {
		if at, ok := s.created.Load(issuanceID); ok && time.Since(at.(time.Time)) < s.claimDelay {
			return IssuanceStatus{Status: StatusPending}, nil
		}
	}
	return IssuanceStatus{Status: StatusIssued, CredentialUUID: "stub-cred-" + issuanceID}, nil
}

// RevokeCredential is a no-op success: the stub keeps no status list, so there
// is nothing to flip (the local ledger still records the revocation).
func (StubIssuer) RevokeCredential(context.Context, string, string) error { return nil }

func (StubIssuer) Ping(context.Context) error { return nil }

func randomID() (string, error) {
	b := make([]byte, stubIssuanceIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("openid4vciissuer: stub id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// StubOfferContent reads what was issued back out of an offer link StubIssuer
// made. ok is false for any other link, including a stub offer made without a
// type, so the caller falls back to its own placeholder.
func StubOfferContent(offerURI string) (StubOffer, bool) {
	rest, found := strings.CutPrefix(offerURI, stubOfferPrefix)
	if !found {
		return StubOffer{}, false
	}
	q, err := url.ParseQuery(rest)
	if err != nil || q.Get(stubParamVCT) == "" {
		return StubOffer{}, false
	}
	claims := map[string]any{}
	if encoded := q.Get(stubParamClaims); encoded != "" {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return StubOffer{}, false
		}
		if err := json.Unmarshal(raw, &claims); err != nil {
			return StubOffer{}, false
		}
	}
	return StubOffer{
		VCT:         q.Get(stubParamVCT),
		DisplayName: q.Get(stubParamName),
		IssuerName:  q.Get(stubParamIssuer),
		Claims:      claims,
	}, true
}
