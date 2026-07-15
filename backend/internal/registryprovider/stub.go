package registryprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// StubRegistry is an in-process KVK stand-in for local dev and tests. It accepts
// a registration request and returns a receipt; in a full run the synthetic
// attestation (BuildStubAttestation) is delivered back over the QERDS inbound
// path. It proves plumbing, NOT compliance. See .ai/features/wallet-bootstrap.md.
type StubRegistry struct{}

// NewStubRegistry returns an in-process registry stub.
func NewStubRegistry() *StubRegistry { return &StubRegistry{} }

// Ping is the boot readiness probe. The in-process stub is always ready.
func (*StubRegistry) Ping(context.Context) error { return nil }

// RequestRegistration accepts the {PID, KVK number} and returns a receipt. The
// attestation is delivered asynchronously; see BuildStubAttestation.
func (*StubRegistry) RequestRegistration(_ context.Context, req RegistrationRequest) (RequestReceipt, error) {
	sum := sha256.Sum256([]byte("kvk|" + req.KVKNumber + "|" + req.PID.FamilyName))
	return RequestReceipt{ProviderRef: "kvk-stub-" + hex.EncodeToString(sum[:8])}, nil
}

// BuildStubAttestation synthesises a deterministic attestation for dev: the
// requester is always a bestuurder, plus one extra claimable co-director. This
// is the payload the QERDS inbound dispatch will hand to wallet.HandleAttestation
// once that wiring lands.
func BuildStubAttestation(req RegistrationRequest) RegistrationAttestation {
	return RegistrationAttestation{
		KVKNumber:                    req.KVKNumber,
		LegalName:                    fmt.Sprintf("Stub Company %s B.V.", req.KVKNumber),
		EUID:                         "NL.KVK." + req.KVKNumber,
		RequesterIsRepresentative:    true,
		RequesterRepresentativeIndex: 0,
		Representatives: []Representative{
			{
				Kind:        KindBestuurder,
				GivenNames:  req.PID.GivenNames,
				FamilyName:  req.PID.FamilyName,
				DateOfBirth: req.PID.DateOfBirth,
				Authority:   AuthoritySole,
			},
			{
				Kind:       KindBestuurder,
				GivenNames: "Sam",
				FamilyName: "Voorbeeld",
				Authority:  AuthorityJointly,
			},
		},
	}
}
