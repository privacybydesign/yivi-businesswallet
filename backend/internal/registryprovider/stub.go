package registryprovider

import (
	"context"
	"fmt"
)

// The demo entry: Yivi B.V. in the Dutch Handelsregister, with Dibran Mulder
// listed as a beperkt volmacht (limited power of attorney).
const yiviKVKNumber = "94861412"

// StubRegistry is an in-process KVK stand-in for local dev and tests. Consult
// returns a synthetic attestation synchronously — it proves the flow, NOT
// compliance (a real KVK integration would deliver the attestation over QERDS).
// See .ai/features/wallet-bootstrap.md.
type StubRegistry struct{}

// NewStubRegistry returns an in-process registry stub.
func NewStubRegistry() *StubRegistry { return &StubRegistry{} }

// Ping is the boot readiness probe. The in-process stub is always ready.
func (*StubRegistry) Ping(context.Context) error { return nil }

// Consult looks up a company by KVK number and returns its registration
// attestation. It hard-codes a realistic entry for Yivi B.V. and falls back to a
// generic company for any other number, so the flow is always demoable.
func (*StubRegistry) Consult(_ context.Context, kvkNumber string) (RegistrationAttestation, error) {
	if kvkNumber == yiviKVKNumber {
		return yiviAttestation(), nil
	}
	return genericAttestation(kvkNumber), nil
}

// yiviAttestation is the demo fixture: Yivi B.V., with the requester (Dibran
// Mulder) as a gevolmachtigde holding a beperkt volmacht.
func yiviAttestation() RegistrationAttestation {
	return RegistrationAttestation{
		KVKNumber:                    yiviKVKNumber,
		LegalName:                    "Yivi B.V.",
		EUID:                         "NL.KVK." + yiviKVKNumber,
		RequesterIsRepresentative:    true,
		RequesterRepresentativeIndex: 0,
		Representatives: []Representative{
			{Kind: KindGevolmachtigde, GivenNames: "Dibran", FamilyName: "Mulder", Authority: AuthorityBeperkt},
		},
	}
}

// genericAttestation lets any other KVK number activate a wallet in dev, with the
// requester as a sole bestuurder.
func genericAttestation(kvkNumber string) RegistrationAttestation {
	return RegistrationAttestation{
		KVKNumber:                    kvkNumber,
		LegalName:                    fmt.Sprintf("Stub Company %s B.V.", kvkNumber),
		EUID:                         "NL.KVK." + kvkNumber,
		RequesterIsRepresentative:    true,
		RequesterRepresentativeIndex: 0,
		Representatives: []Representative{
			{Kind: KindBestuurder, GivenNames: "Sam", FamilyName: "Voorbeeld", Authority: AuthoritySole},
		},
	}
}
