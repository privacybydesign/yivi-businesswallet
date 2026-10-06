package seed

import (
	"slices"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
)

// Every demo flow is one the engine accepts, as its store checks on save.
func TestDemoProofingFlowsAreValid(t *testing.T) {
	for _, o := range demoProofingOrgs {
		for _, f := range o.allFlows() {
			def := f.def
			def.TenantID = "tenant"
			if err := flow.Validate(def); err != nil {
				t.Errorf("%s flow %q: %v", o.slug, f.def.Name, err)
			}
		}
	}
}

// Every flow a demo customer names is one of its org's flows.
func TestDemoCustomersNameOrgFlows(t *testing.T) {
	for _, o := range demoProofingOrgs {
		names := []string{}
		for _, f := range o.allFlows() {
			names = append(names, f.def.Name)
		}
		for _, c := range o.customers {
			for _, name := range c.flowNames() {
				if !slices.Contains(names, name) {
					t.Errorf("%s customer %q names unknown flow %q", o.slug, c.name, name)
				}
			}
		}
	}
}

// demoNameMark is what every seeded real-world name carries, as the proofing
// orgs in seed.go do.
const demoNameMark = " (Demo)"

// Every demo customer's name says "(Demo)": they are real organisations'
// names, used only to show the use cases.
func TestDemoCustomersSayDemo(t *testing.T) {
	for _, o := range demoProofingOrgs {
		for _, c := range o.customers {
			if !strings.HasSuffix(c.name, demoNameMark) {
				t.Errorf("%s customer %q does not end in %q", o.slug, c.name, demoNameMark)
			}
		}
	}
}

// Every demo proofing org is a seeded org.
func TestDemoProofingOrgsAreSeeded(t *testing.T) {
	seeded := slices.Concat(demoOrganizations, proofingOrganizations)
	for _, o := range demoProofingOrgs {
		if !slices.ContainsFunc(seeded, func(d demoOrganization) bool { return d.slug == o.slug }) {
			t.Errorf("proofing org %q is not a seeded org", o.slug)
		}
	}
}

// Every seeded org has its own KVK number: organizations.kvk_number is unique,
// and a duplicate makes the seed's ensureOrg fail.
func TestDemoOrgKVKNumbersUnique(t *testing.T) {
	seen := map[string]string{}
	all := slices.Concat(demoOrganizations, proofingOrganizations)
	for _, c := range communityOrganizations {
		all = append(all, c.org)
	}
	for _, o := range append(all, kvkRegisterOrg) {
		if other, ok := seen[o.kvkNumber]; ok && other != o.slug {
			t.Errorf("KVK number %s is used by %s and %s", o.kvkNumber, other, o.slug)
		}
		seen[o.kvkNumber] = o.slug
	}
}
