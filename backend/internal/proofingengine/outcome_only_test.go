package proofingengine

import (
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A flow that chose the outcome only releases no data: none of what the
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

// A flow that lists no data releases everything its steps collect; only the
// explicit attrOutcomeOnly releases the outcome alone.
func TestRequestedAttributesOfDefaultsToSteps(t *testing.T) {
	steps := []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead, flow.StepFaceVerification}
	want := []string{attrDocument, attrDocumentImage, attrDG11, attrDG2, attrChipChecks, attrSelfie, attrBiometrics}
	if got := requestedAttributesOf(flow.FlowDefinition{Steps: steps}); !slices.Equal(got, want) {
		t.Errorf("no list: requestedAttributesOf = %v, want %v", got, want)
	}
	outcomeOnly := []string{attrOutcomeOnly}
	if got := requestedAttributesOf(flow.FlowDefinition{Steps: steps, RequestedAttributes: outcomeOnly}); !slices.Equal(got, outcomeOnly) {
		t.Errorf("outcome only: requestedAttributesOf = %v, want %v", got, outcomeOnly)
	}
}
