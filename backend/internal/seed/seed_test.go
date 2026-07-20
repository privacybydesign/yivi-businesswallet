package seed

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

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
