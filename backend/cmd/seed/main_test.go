package main

import "testing"

// The proofing demo seed creates organisations named after real companies:
// only on staging or a local stack.
func TestCheckProofingDemo(t *testing.T) {
	for _, tc := range []struct {
		devMode     bool
		environment string
		ok          bool
	}{
		{false, "", false},
		{false, "production", false},
		{false, seedEnvironmentStaging, true},
		{true, "", true},
	} {
		if err := checkProofingDemo(tc.devMode, tc.environment); (err == nil) != tc.ok {
			t.Errorf("checkProofingDemo(%v, %q) = %v, want ok %v", tc.devMode, tc.environment, err, tc.ok)
		}
	}
}
