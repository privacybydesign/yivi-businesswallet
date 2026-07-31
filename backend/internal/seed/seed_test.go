package seed

import (
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// TestPartnerOrgFixturesAreWellFormed guards the staging partner seed fixtures:
// every org must carry a bare address local-part (no "@", else qerdsAddress
// double-appends the domain), slugs and KVK numbers must be unique so no two
// partners collide on ON CONFLICT (slug), every org must have at least one admin,
// and every admin e-mail must parse (EnsurePartnerOrganizations parses them and
// would fail the whole seed otherwise).
func TestPartnerOrgFixturesAreWellFormed(t *testing.T) {
	seenSlug := map[string]bool{}
	seenKVK := map[string]bool{}
	for _, p := range partnerOrganizations {
		o := p.org
		if o.slug == "" {
			t.Errorf("partner org %q has an empty slug", o.name)
		}
		if o.addressLocal == "" || strings.Contains(o.addressLocal, "@") {
			t.Errorf("partner org %q addressLocal = %q, want a bare local-part with no domain", o.slug, o.addressLocal)
		}
		if seenSlug[o.slug] {
			t.Errorf("partner org slug %q is duplicated", o.slug)
		}
		seenSlug[o.slug] = true
		if seenKVK[o.kvkNumber] {
			t.Errorf("partner org KVK number %q is duplicated", o.kvkNumber)
		}
		seenKVK[o.kvkNumber] = true
		if o.repKind != "" {
			t.Errorf("partner org %q should carry no representative (we hold no register identity), got repKind %q", o.slug, o.repKind)
		}
		if len(p.team) == 0 {
			t.Errorf("partner org %q has no team members to make admin", o.slug)
		}
		for _, m := range p.team {
			if _, err := user.ParseEmail(m.email); err != nil {
				t.Errorf("partner org %q member e-mail %q does not parse: %v", o.slug, m.email, err)
			}
		}
	}
}

// TestQerdsAddressUsesConfiguredDomain pins that a seeded org's QERDS address is
// assembled from the configured domain, so staging can seed real addresses
// (qerds.staging.yivi.app) instead of the hardcoded qerds.localhost (issue #104).
func TestQerdsAddressUsesConfiguredDomain(t *testing.T) {
	if got, want := qerdsAddress("yivi", "qerds.staging.yivi.app"), "yivi@qerds.staging.yivi.app"; got != want {
		t.Errorf("qerdsAddress(staging) = %q, want %q", got, want)
	}
	if got, want := qerdsAddress("yivi", "qerds.localhost"), "yivi@qerds.localhost"; got != want {
		t.Errorf("qerdsAddress(local) = %q, want %q", got, want)
	}
}

// TestDemoOrgAddressLocalPartsHaveNoDomain guards that the org fixtures store only
// the local-part: a stray "@domain" here would be double-appended by qerdsAddress
// and would also pin the domain back to a literal, defeating the fix.
func TestDemoOrgAddressLocalPartsHaveNoDomain(t *testing.T) {
	orgs := append([]demoOrganization{kvkRegisterOrg}, demoOrganizations...)
	for _, o := range orgs {
		if o.addressLocal == "" {
			t.Errorf("demo org %q has an empty addressLocal", o.slug)
		}
		if strings.Contains(o.addressLocal, "@") {
			t.Errorf("demo org %q addressLocal = %q, want a bare local-part with no domain", o.slug, o.addressLocal)
		}
	}
}

// TestDemoOrgsMatchRegister guards the reconciliation: every seeded demo company's
// KVK identity and primary representative must exist in the register's fake API
// (registryprovider.DemoRegistrations), so a seeded user can open a wallet and
// match a real representative and the two never drift.
func TestDemoOrgsMatchRegister(t *testing.T) {
	data := registryprovider.DefaultDataset()

	for _, o := range demoOrganizations {
		reg, ok := data[o.kvkNumber]
		if !ok {
			t.Errorf("demo org %q (kvk %s) is not in the register dataset", o.slug, o.kvkNumber)
			continue
		}
		if reg.LegalName != o.name || reg.EUID != o.euid {
			t.Errorf("demo org %q identity = %q/%q, register has %q/%q", o.slug, o.name, o.euid, reg.LegalName, reg.EUID)
		}

		want := identity.Name{GivenNames: o.repGiven, LastName: o.repFamily}
		matched := false
		for _, rep := range reg.Representatives {
			stored := identity.Name{GivenNames: rep.GivenNames, LastName: rep.FamilyName}
			if identity.Reconcile(want, &stored) != identity.Review &&
				rep.DateOfBirth == o.repDOB && rep.Kind == o.repKind && rep.Authority == o.repAuth {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("demo org %q primary representative %s %s (%s/%s, dob %s) has no matching register entry",
				o.slug, o.repGiven, o.repFamily, o.repKind, o.repAuth, o.repDOB)
		}
	}
}

// TestRegisterOnlyCompanyIsOpenable is the regression for the unreachable
// open-wallet happy path: at least one validatable KVK number must NOT be seeded
// as an organisation, otherwise every validated requester is bounced with
// ErrAlreadyRegistered when OpenWallet tries to create the org. It also pins the
// register-only demo company (OpenableKVKNumber) as that openable entry.
func TestRegisterOnlyCompanyIsOpenable(t *testing.T) {
	seededKVK := map[string]bool{registryprovider.RegisterKVKNumber: true}
	for _, o := range demoOrganizations {
		seededKVK[o.kvkNumber] = true
	}

	if seededKVK[registryprovider.OpenableKVKNumber] {
		t.Fatalf("register-only company %s must not be seeded as an org", registryprovider.OpenableKVKNumber)
	}
	if _, ok := registryprovider.DefaultDataset()[registryprovider.OpenableKVKNumber]; !ok {
		t.Fatalf("register-only company %s must be a consultable register entry", registryprovider.OpenableKVKNumber)
	}

	openable := 0
	for kvk := range registryprovider.DefaultDataset() {
		if !seededKVK[kvk] {
			openable++
		}
	}
	if openable == 0 {
		t.Fatal("no validatable KVK number is openable: every register entry is already seeded as an org, so OpenWallet's positive path is unreachable")
	}
}

// TestKVKRegisterOrgNotConsultable guards that the KVK register participant is not
// itself a consultable company in the dataset.
func TestKVKRegisterOrgNotConsultable(t *testing.T) {
	if _, ok := registryprovider.DefaultDataset()[registryprovider.RegisterKVKNumber]; ok {
		t.Fatalf("kvk register number %s must not be a consultable company", registryprovider.RegisterKVKNumber)
	}
	if kvkRegisterOrg.repKind != "" {
		t.Fatalf("kvk register org should have no representative, got kind %q", kvkRegisterOrg.repKind)
	}
}
