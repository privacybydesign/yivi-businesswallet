package seed

import (
	"slices"
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

// A demo org's default flow is one of its own flows, not a data request flow.
func TestDemoDefaultFlowIsOrgFlow(t *testing.T) {
	for _, o := range demoProofingOrgs {
		if !slices.ContainsFunc(o.flows, func(f demoProofingFlow) bool { return f.def.Name == o.defaultFlow }) {
			t.Errorf("%s default flow %q is not one of its flows", o.slug, o.defaultFlow)
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
