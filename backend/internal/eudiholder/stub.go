package eudiholder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const stubRefBytes = 16

// stubReceivedVCT is the placeholder credential type the stub records for a
// redeemed offer — the stub does not run a real OpenID4VCI flow, so there is no
// issuer-supplied vct to read.
const stubReceivedVCT = "eaa.received.stub"

// stubReceivedPayload is a synthetic verified SD-JWT payload the stub attaches to
// a redeemed credential so the held-credential detail view has attributes to show
// in local dev / CI (the real receive flow supplies the issuer's payload). It
// carries a couple of demo attributes plus the registered claims Claims strips.
const stubReceivedPayload = `{"vct":"eaa.received.stub","iss":"stub-issuer","company_name":"Demo Supplier B.V.","kvk_number":"12345678","approval_status":"approved"}`

// stubReceivedLifetime is how long the stub's synthesised credential stays valid —
// one year, the same default the reference issuer mints with — so a credential
// received in local dev / CI carries an exp claim like a real one does and the held
// view has an expiry to show.
const stubReceivedLifetime = 365 * 24 * time.Hour

// StubHolder is an in-process, in-memory holder engine for local dev / CI
// (ATTESTATION_HOLDER=stub, the default). It keeps credentials in a map keyed by
// org so the store/index/delete loop runs offline without irmago or Postgres. It
// is the holder equivalent of openid4vciissuer.StubIssuer. Selected by config,
// never code.
type StubHolder struct {
	mu    sync.Mutex
	creds map[uuid.UUID]map[string]Credential
}

func NewStubHolder() *StubHolder {
	return &StubHolder{creds: make(map[uuid.UUID]map[string]Credential)}
}

func (*StubHolder) Ping(context.Context) error { return nil }

func (h *StubHolder) Store(_ context.Context, orgID uuid.UUID, cred Credential) (string, error) {
	ref, err := randomRef()
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.creds[orgID] == nil {
		h.creds[orgID] = make(map[string]Credential)
	}
	h.creds[orgID][ref] = cred
	return ref, nil
}

// Redeem synthesises a stored credential instead of running a real OpenID4VCI
// flow, so the QERDS receive → held-index loop runs offline under the stub
// (dev/CI default). offerURI is used only as a stable dedup hash. It mirrors the
// engine contract: on return the credential is "held" and the ref can be indexed.
func (h *StubHolder) Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (Redeemed, error) {
	issuedAt := time.Now()
	expiresAt := issuedAt.Add(stubReceivedLifetime)
	cred := Credential{
		VCT:              stubReceivedVCT,
		IssuerURL:        offerURI,
		CredentialIssuer: offerURI,
		Hash:             offerURI,
		ProcessedPayload: []byte(stubReceivedPayload),
		IssuedAt:         issuedAt,
		ExpiresAt:        &expiresAt,
	}
	ref, err := h.Store(ctx, orgID, cred)
	if err != nil {
		return Redeemed{}, err
	}
	return Redeemed{Ref: ref, VCT: cred.VCT, Issuer: cred.CredentialIssuer}, nil
}

// Claims decodes the stored credential's processed payload into its disclosed
// attributes, resolving by ref then falling back to vct — but only when the vct
// names exactly one held credential, mirroring the engine (see
// Engine.batchByUniqueVCT). An unknown org/ref/vct, or a vct the org holds several
// credentials of, yields an empty map (matches the engine contract, so the
// held-detail flow behaves the same under stub and irmago).
// The stub records no credential-level display metadata, so lang is accepted for
// interface parity but ignored (DisplayName / LogoURI stay empty and the caller
// falls back to the VCT-derived name and shows no logo).
func (h *StubHolder) Claims(_ context.Context, orgID uuid.UUID, ref, vct, _ string) (HeldCredential, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.creds[orgID][ref]
	if !ok && vct != "" {
		matches := 0
		for _, cred := range h.creds[orgID] {
			if cred.VCT == vct {
				matches++
				found = cred
			}
		}
		ok = matches == 1
	}
	if !ok {
		return HeldCredential{Attributes: []HeldAttribute{}}, nil
	}
	attributes, err := assembleAttributes(found.ProcessedPayload, nil, nil)
	if err != nil {
		return HeldCredential{}, err
	}
	// The stub records no issuer display metadata, so IssuerName is left empty and
	// the caller falls back to the issuer identifier.
	return HeldCredential{Attributes: attributes}, nil
}

// Displays returns an entry per held vct with empty display metadata: the stub
// records no credential-level type-metadata, so every held-list row falls back to
// its VCT-derived name and shows no logo (matches the engine contract shape).
func (h *StubHolder) Displays(_ context.Context, orgID uuid.UUID, _ string) (map[string]HeldDisplay, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	displays := make(map[string]HeldDisplay)
	for _, cred := range h.creds[orgID] {
		if cred.VCT != "" {
			displays[cred.VCT] = HeldDisplay{}
		}
	}
	return displays, nil
}

// Validities returns each stored credential's expiry keyed by its ref. The stub
// runs no Token Status List check, so no credential is ever reported revoked (the
// engine reports the last bit it stored; the stub stores none).
func (h *StubHolder) Validities(_ context.Context, orgID uuid.UUID) (map[string]HeldValidity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	validities := make(map[string]HeldValidity, len(h.creds[orgID]))
	for ref, cred := range h.creds[orgID] {
		validities[ref] = HeldValidity{ExpiresAt: cred.ExpiresAt}
	}
	return validities, nil
}

// Delete removes the credential; an absent ref is a no-op (matches the engine
// contract, so the held-delete flow behaves the same under stub and irmago).
func (h *StubHolder) Delete(_ context.Context, orgID uuid.UUID, ref string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.creds[orgID], ref)
	return nil
}

func (h *StubHolder) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.creds = make(map[uuid.UUID]map[string]Credential)
	return nil
}

func randomRef() (string, error) {
	b := make([]byte, stubRefBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("eudiholder: stub ref: %w", err)
	}
	return hex.EncodeToString(b), nil
}
