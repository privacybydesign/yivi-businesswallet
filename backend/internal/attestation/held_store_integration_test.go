//go:build integration

package attestation_test

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

// Two re-checks that both see one status move audit it once; the move back is
// a new change.
func TestHeldStatusChangeRecordedOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	held, err := e.store.RecordHeld(ctx, e.orgID, attestation.HeldInput{
		CredentialRef: "ref-1", VCT: "eaa.supplier", Issuer: "https://issuer.test", Source: attestation.HeldSourceQERDS,
	})
	if err != nil {
		t.Fatalf("RecordHeld: %v", err)
	}

	steps := []struct {
		status attestation.HeldStatus
		want   bool
	}{
		{attestation.HeldValid, false},
		{attestation.HeldRevoked, true},
		{attestation.HeldRevoked, false},
		{attestation.HeldValid, true},
	}
	for i, step := range steps {
		recorded, err := e.store.RecordHeldStatusChange(ctx, e.orgID, held.ID, held.VCT, step.status)
		if err != nil {
			t.Fatalf("step %d: RecordHeldStatusChange: %v", i, err)
		}
		if recorded != step.want {
			t.Errorf("step %d: recorded = %v, want %v", i, recorded, step.want)
		}
	}
}
