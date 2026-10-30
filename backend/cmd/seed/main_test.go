package main

import "testing"

// The proofing and bewind demo seeds create organisations named after real
// institutions: only on staging or a local stack.
func TestCheckStagingOnly(t *testing.T) {
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
		if err := checkStagingOnly("--proofing-demo", tc.devMode, tc.environment); (err == nil) != tc.ok {
			t.Errorf("checkStagingOnly(%v, %q) = %v, want ok %v", tc.devMode, tc.environment, err, tc.ok)
		}
	}
}
