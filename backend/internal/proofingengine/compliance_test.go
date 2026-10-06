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
