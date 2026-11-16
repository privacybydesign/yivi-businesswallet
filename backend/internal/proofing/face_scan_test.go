package proofing

import (
	"errors"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// A face checked in the document scan runs in the browser, and only with the
// document photo.
func TestNormalizeFlowScannedFace(t *testing.T) {
	in := proofingprovider.FlowSpec{Name: "Photo and face", Steps: []string{stepDocumentPhoto, "face_verification"}, FaceProvider: faceProviderIDScan}
	out, err := normalizeFlow(in)
	if err != nil || out.SelfieLocation != selfieLocationBrowser || out.FaceProvider != faceProviderIDScan {
		t.Fatalf("normalizeFlow = %+v, %v; want ID Scan in the browser", out, err)
	}
	if !flowCompletable(proofingprovider.Flow{FlowSpec: out}) {
		t.Error("a face checked in the scan needs no chip photo, yet the flow is not completable")
	}
	if flowNeedsReferencePhoto(proofingprovider.Flow{FlowSpec: out}) {
		t.Error("a face checked in the scan asks for a reference photo")
	}

	in.Steps = []string{"face_verification"}
	if _, err := normalizeFlow(in); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("without the document photo: %v, want ErrInvalidInput", err)
	}
}

// editorFlow is in as CreateFlow saves it, or edits current with it.
func editorFlow(in proofingprovider.FlowSpec, current *proofingprovider.FlowSpec) (proofingprovider.FlowSpec, error) {
	out, err := normalizeFlow(in)
	if err != nil {
		return out, err
	}
	return checkEditorChoices(out, current)
}

// The API refuses what the flow editor never lets an admin save.
func TestFlowEditorRules(t *testing.T) {
	chipAndFace := []string{stepDocumentCapture, stepNFCRead, stepFaceVerification}
	cases := map[string]struct {
		spec proofingprovider.FlowSpec
		ok   bool
	}{
		"chip data with the chip read":      {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeDG11, attributeSelfie}}, true},
		"chip data without the chip read":   {proofingprovider.FlowSpec{Steps: []string{stepDocumentPhoto}, RequestedAttributes: []string{attributeDG11}}, false},
		"selfie without a face step":        {proofingprovider.FlowSpec{Steps: []string{stepDocumentCapture, stepNFCRead}, RequestedAttributes: []string{attributeSelfie}}, false},
		"document photos without the photo": {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeDocumentImage}}, false},
		"an item the editor does not offer": {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeFaceImage}}, false},
		"outcome only next to an item":      {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeOutcomeOnly, attributeDocument}}, false},
		"age the Yivi credentials answer":   {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeAgeOver}, AgeOver: 18}, true},
		"age they carry no claim for":       {proofingprovider.FlowSpec{Steps: chipAndFace, RequestedAttributes: []string{attributeAgeOver}, AgeOver: 30}, false},
		"Yivi only at low":                  {proofingprovider.FlowSpec{Steps: chipAndFace, Apps: []string{appYivi}, RequiredAssuranceLevel: eidasLow}, true},
		"Yivi only at substantial":          {proofingprovider.FlowSpec{Steps: chipAndFace, Apps: []string{appYivi}, RequiredAssuranceLevel: eidasSubstantial}, false},
		"Yivi only, face without the chip":  {proofingprovider.FlowSpec{Steps: []string{stepFaceVerification}, Apps: []string{appYivi}}, false},
		"both apps, face without the chip":  {proofingprovider.FlowSpec{Steps: []string{stepFaceVerification}}, true},
		"a separate face step":              {proofingprovider.FlowSpec{Steps: []string{"selfie"}}, false},
		"a step named twice":                {proofingprovider.FlowSpec{Steps: []string{stepNFCRead, stepDocumentCapture, stepNFCRead}}, false},
		"a threshold on the slider":         {proofingprovider.FlowSpec{Steps: chipAndFace, CheckThresholds: map[string]float64{checkFaceMatch: 0.29}}, true},
		"a threshold between its steps":     {proofingprovider.FlowSpec{Steps: chipAndFace, CheckThresholds: map[string]float64{checkFaceMatch: 0.755}}, false},
		"a retention the editor offers":     {proofingprovider.FlowSpec{Steps: chipAndFace, RetentionOverrideSeconds: 30 * secondsPerDay}, true},
		"a retention it does not":           {proofingprovider.FlowSpec{Steps: chipAndFace, RetentionOverrideSeconds: 7 * secondsPerDay}, false},
		"a document kind":                   {proofingprovider.FlowSpec{Steps: chipAndFace, AcceptedDocumentTypes: []string{proofingprovider.DocumentKindPassport}}, true},
		"a document code":                   {proofingprovider.FlowSpec{Steps: chipAndFace, AcceptedDocumentTypes: []string{"P"}}, false},
		"a legal basis":                     {proofingprovider.FlowSpec{Steps: chipAndFace, LegalBasis: legalBasisLegalObligation}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.spec.Name = name
			_, err := editorFlow(c.spec, nil)
			if c.ok && err != nil {
				t.Fatalf("editorFlow = %v, want accepted", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("editorFlow = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// The steps go in the order the Idem app runs them, ID Scan always checks
// liveness, and every document kind is any document.
func TestFlowEditorNormalizes(t *testing.T) {
	in := proofingprovider.FlowSpec{
		Name: "Scan", Steps: []string{stepFaceVerification, stepDocumentPhoto}, FaceProvider: faceProviderIDScan,
		RequiredChecks: []string{checkFaceMatch}, AcceptedDocumentTypes: slices.Clone(documentKindNames),
	}
	out, err := editorFlow(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Steps, []string{stepDocumentPhoto, stepFaceVerification}) {
		t.Errorf("steps = %v, want the document photo first", out.Steps)
	}
	if !slices.Contains(out.RequiredChecks, checkFaceLiveness) {
		t.Errorf("checks = %v, want face.liveness with ID Scan", out.RequiredChecks)
	}
	if len(out.AcceptedDocumentTypes) != 0 {
		t.Errorf("document types = %v, want any document", out.AcceptedDocumentTypes)
	}
}

// An edit keeps what the version has, as the editor does, though the editor
// offers no choice for it.
func TestFlowEditorKeepsStoredValues(t *testing.T) {
	current := proofingprovider.FlowSpec{
		RequestedAttributes: []string{attributeAgeOver}, AgeOver: 17, RetentionOverrideSeconds: 7 * secondsPerDay,
		AcceptedDocumentTypes: []string{"P"}, LegalBasis: legalBasisLegalObligation, ProcessingPurpose: "Onboarding",
	}
	in := current
	in.Name, in.Steps, in.LegalBasis, in.ProcessingPurpose = "Edited", []string{stepDocumentCapture, stepNFCRead}, "", ""
	out, err := editorFlow(in, &current)
	if err != nil {
		t.Fatal(err)
	}
	if out.LegalBasis != current.LegalBasis || out.ProcessingPurpose != current.ProcessingPurpose {
		t.Errorf("edit = %q %q, want the version's legal basis and purpose", out.LegalBasis, out.ProcessingPurpose)
	}
	in.LegalBasis = "consent"
	if _, err := editorFlow(in, &current); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a changed legal basis: %v, want ErrInvalidInput", err)
	}
}
