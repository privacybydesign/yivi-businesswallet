package eudiholder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"
)

// stubDisplayLang is the single locale the stub localizes its synthetic display
// strings under (real credentials carry issuer-supplied localizations).
const stubDisplayLang = "en"

const stubRefBytes = 16

// stubReceivedVCT is the placeholder credential type the stub records for a
// redeemed offer — the stub does not run a real OpenID4VCI flow, so there is no
// issuer-supplied vct to read.
const stubReceivedVCT = "eaa.received.stub"

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
	cred := Credential{
		VCT:              stubReceivedVCT,
		IssuerURL:        offerURI,
		CredentialIssuer: offerURI,
		Hash:             offerURI,
		IssuedAt:         time.Now(),
	}
	ref, err := h.Store(ctx, orgID, cred)
	if err != nil {
		return Redeemed{}, err
	}
	return Redeemed{Ref: ref, VCT: cred.VCT, Issuer: cred.CredentialIssuer}, nil
}

// List returns the org's stored credentials as the clientmodels display model,
// synthesised from the in-memory credentials so the read/display path renders
// under the stub (dev/CI). Real display strings come from issuer metadata; the
// stub fills name/issuer from what it holds.
func (h *StubHolder) List(_ context.Context, orgID uuid.UUID) ([]*clientmodels.Credential, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	creds := make([]*clientmodels.Credential, 0, len(h.creds[orgID]))
	for ref, c := range h.creds[orgID] {
		creds = append(creds, &clientmodels.Credential{
			CredentialId:          c.VCT,
			Hash:                  c.Hash,
			Name:                  clientmodels.TranslatedString{stubDisplayLang: c.VCT},
			Issuer:                clientmodels.TrustedParty{Id: c.CredentialIssuer, Name: clientmodels.TranslatedString{stubDisplayLang: c.CredentialIssuer}},
			CredentialInstanceIds: map[clientmodels.CredentialFormat]string{clientmodels.Format_SdJwtVc: ref},
		})
	}
	return creds, nil
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
