package walletunit

import (
	"context"
	"crypto/ecdsa"
	"time"

	"github.com/google/uuid"
)

// stubStatusIndex is the one status entry every stub attestation points at; the
// stub's lists read it as valid.
const stubStatusIndex = 0

// StubAttester issues wallet unit attestations without a store, under a signer
// nobody trusts: for local dev and CI. Nothing is recorded, so revocation has
// no effect and every status list reads all valid.
type StubAttester struct {
	orgs   orgReader
	signer *Signer
	cfg    Config
	now    func() time.Time
}

var _ Attester = (*StubAttester)(nil)

func NewStubAttester(orgs orgReader, signer *Signer, cfg Config) *StubAttester {
	return &StubAttester{orgs: orgs, signer: signer, cfg: cfg.normalized(), now: time.Now}
}

func (s *StubAttester) IssueBWIA(ctx context.Context, orgID uuid.UUID, instanceKey *ecdsa.PublicKey) (string, error) {
	org, cnf, err := bwiaSubject(ctx, s.orgs, orgID, instanceKey)
	if err != nil {
		return "", err
	}
	return s.signer.sign(bwiaTyp, s.cfg.bwiaClaims("stub-"+orgID.String(), org, cnf, stubStatusIndex, s.now()))
}

func (s *StubAttester) IssueSKA(ctx context.Context, orgID uuid.UUID, keys []*ecdsa.PublicKey, nonce *string) (string, error) {
	if s.cfg.SKA == nil || len(keys) == 0 {
		return "", nil
	}
	attested, err := skaSubject(ctx, s.orgs, orgID, keys)
	if err != nil {
		return "", err
	}
	return s.signer.sign(skaTyp, s.cfg.skaClaims(attested, stubStatusIndex, s.now(), nonce))
}

func (s *StubAttester) StatusList(_ context.Context, list int64) (string, error) {
	if list < 0 || list >= statusListCount {
		return "", ErrUnknownList
	}
	lst, err := encodeStatusList(nil)
	if err != nil {
		return "", err
	}
	return s.signer.sign(statusListTyp, s.cfg.statusListClaims(list, lst, s.now()))
}
