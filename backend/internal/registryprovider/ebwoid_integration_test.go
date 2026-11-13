//go:build integration

package registryprovider

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestStubEBWOIDIssuerStoresInTheIrmagoEngine(t *testing.T) {
	_, dsn := testdb.Fresh(t)
	var key [32]byte
	eng := eudiholder.NewEngine(dsn, t.TempDir(), key, eudiholder.RedeemConfig{})
	t.Cleanup(func() { _ = eng.Close() })
	ctx := context.Background()
	orgID := uuid.New()
	att := RegistrationAttestation{LegalName: "Zonnedael B.V.", EUID: "NLNHR.90000001"}

	held, err := StubEBWOIDIssuer{}.Obtain(ctx, eng, orgID, att)
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	cred, err := eng.Claims(ctx, orgID, held.Ref, EBWOIDVCT, "")
	if err != nil {
		t.Fatalf("Claims: %v", err)
	}
	claims := map[string]any{}
	for _, a := range cred.Attributes {
		claims[a.Key] = a.Value
	}
	if claims[EBWOIDClaimID] != att.EUID || claims[EBWOIDClaimName] != att.LegalName {
		t.Fatalf("claims = %v, want id %q and name %q", claims, att.EUID, att.LegalName)
	}
}
