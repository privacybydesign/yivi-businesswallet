package eudiholder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

const stubRefBytes = 16

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
