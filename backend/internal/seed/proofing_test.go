package seed

import (
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
)

// Every demo flow is one the engine accepts, as its store checks on save.
func TestDemoProofingFlowsAreValid(t *testing.T) {
	for _, f := range demoProofingFlows {
		def := f.def
		def.TenantID = "tenant"
		if err := flow.Validate(def); err != nil {
			t.Errorf("flow %q: %v", f.def.Name, err)
		}
	}
}

// Every flow a demo customer names is a demo flow.
func TestDemoProofingCustomersNameDemoFlows(t *testing.T) {
	names := []string{}
	for _, f := range demoProofingFlows {
		names = append(names, f.def.Name)
	}
	for _, c := range demoProofingCustomers {
		for _, name := range append(slices.Clone(c.flows), dataRequestFlows...) {
			if !slices.Contains(names, name) {
				t.Errorf("customer %q names unknown flow %q", c.name, name)
			}
		}
	}
}
