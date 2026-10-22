package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
)

// The disclosed credential, not its claims, tells a residence permit apart.
func TestDisclosureDocumentKind(t *testing.T) {
	for credential, want := range map[string]string{
		"pbdf-staging.pbdf.passport":        flow.DocumentKindPassport,
		"pbdf-staging.pbdf.idcard":          flow.DocumentKindIdentityCard,
		"pbdf-staging.pbdf.residencepermit": flow.DocumentKindResidencePermit,
		"pbdf-staging.sidn-pbdf.email":      "",
	} {
		if got := documentFromDisclosure(disclosureInfo{Credential: credential}).Kind; got != want {
			t.Errorf("documentFromDisclosure(%s).Kind = %q, want %q", credential, got, want)
		}
	}
}
