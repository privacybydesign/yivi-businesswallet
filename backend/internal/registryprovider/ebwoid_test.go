package registryprovider

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func TestStubEBWOIDIssuerStoresTheRegisterIdentity(t *testing.T) {
	h := eudiholder.NewStubHolder()
	orgID := uuid.New()
	att := RegistrationAttestation{LegalName: "Zonnedael B.V.", EUID: "NLNHR.90000001"}

	held, err := StubEBWOIDIssuer{}.Obtain(context.Background(), h, orgID, att)
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	cred, err := h.Claims(context.Background(), orgID, held.Ref, EBWOIDVCT, "")
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
