package registryprovider

import (
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
)

// RegisterKVKNumber, RegisterSlug and RegisterLegalName identify the KVK register
// itself as a business-wallet participant. The KVK-side match/no-match decisions
// are audited against this organisation's log (see SeededRegistry.Consult); the
// seed provisions it. It is deliberately not one of the consultable companies.
const (
	RegisterKVKNumber = "90000000"
	RegisterSlug      = "kvk"
	RegisterLegalName = "Kamer van Koophandel"
	RegisterEUID      = "NL.KVK." + RegisterKVKNumber
)

// Registration is one company's entry in KVK's register — a single "row" of the
// seeded fake API. The authoritative demo data lives in DemoRegistrations; the
// seed reconciles the demo organisations against it so a seeded user can open a
// wallet and match a real representative.
type Registration struct {
	KVKNumber       string
	LegalName       string
	EUID            string
	Representatives []Representative
}

// DemoRegistrations is the seeded fake KVK API: the deterministic set of known
// company registrations the register matches consults against. The KVK numbers
// and primary representatives are the single source of truth the seed reuses for
// its demo organisations, so the seeded data and the register flow never drift.
var DemoRegistrations = []Registration{
	{
		KVKNumber: "90000010", LegalName: "Yivi B.V.", EUID: "NL.KVK.90000010",
		Representatives: []Representative{
			{Kind: KindBestuurder, GivenNames: "Johannes Hendrik", FamilyName: "Janssen", DateOfBirth: "1979-05-14", Authority: AuthoritySole},
			{Kind: KindGevolmachtigde, GivenNames: "Dibran", FamilyName: "Mulder", DateOfBirth: "1988-09-03", Authority: AuthorityBeperkt},
		},
	},
	{
		KVKNumber: "90000020", LegalName: "Firsty.app B.V.", EUID: "NL.KVK.90000020",
		Representatives: []Representative{
			{Kind: KindBestuurder, GivenNames: "Thijs Adriaan", FamilyName: "de Vries", DateOfBirth: "1985-11-22", Authority: AuthorityJointly},
		},
	},
	{
		KVKNumber: "90000030", LegalName: "Radboud Universiteit", EUID: "NL.KVK.90000030",
		Representatives: []Representative{
			{Kind: KindGevolmachtigde, GivenNames: "Anke", FamilyName: "Bakker", DateOfBirth: "1990-02-17", Authority: AuthorityBeperkt},
		},
	},
}

// Dataset is an in-memory index of registrations keyed by KVK number: the
// consultable state of the fake authentic source.
type Dataset map[string]Registration

// DefaultDataset builds the dataset from DemoRegistrations.
func DefaultDataset() Dataset {
	d := make(Dataset, len(DemoRegistrations))
	for _, r := range DemoRegistrations {
		d[r.KVKNumber] = r
	}
	return d
}

// match reports whether the request's identification data matches one of the
// registration's representatives, returning that representative's index. Matching
// reuses identity.Name.Key/Reconcile (accent- and case-insensitive) on name, and
// — when both sides carry a birth date — additionally requires it to be equal,
// mirroring the §8 co-owner matching (name + date of birth, stronger than name
// alone). This is the KVK-side decision: our code does not decide who represents
// a company, it asks the register.
func (r Registration) match(req ConsultRequest) (int, bool) {
	want := identity.Name{GivenNames: req.GivenNames, LastName: req.FamilyName}
	for i, rep := range r.Representatives {
		stored := identity.Name{GivenNames: rep.GivenNames, LastName: rep.FamilyName}
		if identity.Reconcile(want, &stored) == identity.Review {
			continue
		}
		if req.DateOfBirth != "" && rep.DateOfBirth != "" && req.DateOfBirth != rep.DateOfBirth {
			continue
		}
		return i, true
	}
	return 0, false
}
