package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A flow that requests no data releases the outcome only: none of what the
// steps collected reaches the result, as the subject is told.
func TestOutcomeOnlyReleasesNoData(t *testing.T) {
	req := liveRegulaMatch()
	req.Document = &documentInfo{IssuingState: "NLD"}
	req.Selfie = &photoInfo{ImageBase64: "c2VsZmll", MimeType: "image/jpeg"}
	checks := verifiedChip()

	outcomeOnly := session.Session{RequestedAttributes: []string{attrOutcomeOnly}}
	// The assurance summary is the outcome itself; everything else is data.
	for key := range buildResult(outcomeOnly, req, checks, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{}) {
		if key != "assurance" {
			t.Errorf("outcome-only result carries %q, want the assurance summary only", key)
		}
	}

	// The same evidence on a session listing the document data releases it.
	document := session.Session{RequestedAttributes: []string{attrDocument}}
	if _, ok := buildResult(document, req, checks, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})["document"]; !ok {
		t.Error("a session requesting the document data did not get it")
	}
}
