package walletunit

import (
	"context"
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

func (s *StubAttester) Issue(ctx context.Context, orgID uuid.UUID, req IssueRequest) (Issued, error) {
	org, cnf, attested, err := s.cfg.prepare(ctx, s.orgs, orgID, req)
	if err != nil {
		return Issued{}, err
	}
	now := s.now()
	var out Issued
	if out.BWIA, err = s.signer.sign(bwiaTyp, s.cfg.bwiaClaims("stub-"+orgID.String(), org, cnf, stubStatusIndex, now)); err != nil {
		return Issued{}, err
	}
	if len(attested) == 0 {
		return out, nil
	}
	if out.SKA, err = s.signer.sign(skaTyp, s.cfg.skaClaims(attested, stubStatusIndex, now)); err != nil {
		return Issued{}, err
	}
	return out, nil
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
