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

// The BRP and the Yivi document credentials disclose DD-MM-YYYY; the result
// gives every date as YYYY-MM-DD, whatever the source.
func TestDisclosureDatesAreISO(t *testing.T) {
	for raw, want := range map[string]string{
		"24-10-2001": "2001-10-24",
		"2001-10-24": "2001-10-24",
		"":           "",
		"oct 2001":   "oct 2001",
	} {
		doc := documentFromDisclosure(disclosureInfo{Attributes: map[string]string{"dateOfBirth": raw, "dateOfExpiry": raw}})
		if doc.DateOfBirth != want || doc.DateOfExpiry != want {
			t.Errorf("dates disclosed as %q = %q and %q, want %q", raw, doc.DateOfBirth, doc.DateOfExpiry, want)
		}
	}
}
