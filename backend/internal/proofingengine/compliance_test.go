package proofingengine

import (
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A document is valid through its expiry day, and expired the day after; an
// expiry that does not parse reads as expired.
func TestDocumentExpired(t *testing.T) {
	now := time.Date(2026, 10, 5, 23, 30, 0, 0, time.UTC)
	for expiry, want := range map[string]bool{
		"2026-10-06": false, "2026-10-05": false, "2026-10-04": true, "": true, "2026-13-01": true,
	} {
		if got := documentExpired(expiry, now); got != want {
			t.Errorf("documentExpired(%q) = %v, want %v", expiry, got, want)
		}
	}
}

// A session whose document is past its expiry date is rejected DOC_EXPIRED,
// whatever else passed; one valid through today is not.
func TestComplianceRejectsExpired(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	fd := &flow.FlowDefinition{Steps: []flow.Step{flow.StepNFCRead}}
	for expiry, want := range map[string]string{"2026-10-04": errCodeDocExpired, "2026-10-05": "", "2031-01-01": ""} {
		req := appResultRequest{Document: &documentInfo{DateOfExpiry: expiry}}
		if _, code := flowComplianceFailure(fd, session.Session{}, req, now); code != want {
			t.Errorf("expiry %s: error code %q, want %q", expiry, code, want)
		}
	}
}

// A Yivi disclosure on a flow that restricts the document type or issuing
// country is refused unless it shows a compliant one, and a disclosed expiry
// in the past is refused; a flow without restrictions accepts it.
func TestDisclosureCompliance(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	nldPassports := &flow.FlowDefinition{AcceptedDocumentTypes: []string{"P"}, AcceptedIssuingCountries: []string{"NLD"}}
	for name, c := range map[string]struct {
		fd   *flow.FlowDefinition
		doc  documentInfo
		want string
	}{
		"no flow":                {nil, documentInfo{}, ""},
		"unrestricted":           {&flow.FlowDefinition{}, documentInfo{}, ""},
		"type not disclosed":     {nldPassports, documentInfo{IssuingState: "NLD"}, errCodeDocTypeRefused},
		"country not disclosed":  {nldPassports, documentInfo{Type: "P"}, errCodeCountryRefused},
		"other country":          {nldPassports, documentInfo{Type: "P", IssuingState: "DEU"}, errCodeCountryRefused},
		"compliant":              {nldPassports, documentInfo{Type: "P", IssuingState: "NLD"}, ""},
		"expired":                {&flow.FlowDefinition{}, documentInfo{DateOfExpiry: "2026-10-04"}, errCodeDocExpired},
		"expired, dd-mm-yyyy":    {&flow.FlowDefinition{}, documentInfo{DateOfExpiry: "04-10-2026"}, errCodeDocExpired},
		"valid today":            {&flow.FlowDefinition{}, documentInfo{DateOfExpiry: "2026-10-05"}, ""},
		"expiry in other format": {&flow.FlowDefinition{}, documentInfo{DateOfExpiry: "20261004"}, ""},
	} {
		if _, code := disclosureComplianceFailure(c.fd, c.doc, now); code != c.want {
			t.Errorf("%s: error code %q, want %q", name, code, c.want)
		}
	}
}
