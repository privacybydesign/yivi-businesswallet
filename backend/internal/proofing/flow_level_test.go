package proofing

import (
	"errors"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// A flow gets what its level needs and keeps the rest; none needs nothing and
// high is refused.
func TestWithLevelNeeds(t *testing.T) {
	for _, level := range []string{"", flowLevelNone} {
		in := proofingprovider.FlowSpec{Name: "Photo", Steps: []string{stepDocumentPhoto}, RequiredAssuranceLevel: level}
		got, err := withLevelNeeds(in)
		if err != nil || got.RequiredAssuranceLevel != "" || !slices.Equal(got.Steps, in.Steps) || len(got.RequiredChecks) != 0 {
			t.Errorf("level %q = %+v, %v; want the flow as sent, without a level", level, got, err)
		}
	}

	low, err := withLevelNeeds(proofingprovider.FlowSpec{
		Name: "Low", RequiredAssuranceLevel: eidasLow,
		AcceptedIssuingCountries: []string{"NLD"},
	})
	if err != nil || !slices.Equal(low.Steps, []string{stepDocumentCapture, stepNFCRead}) ||
		!slices.Equal(low.RequiredChecks, []string{"nfc.passive_auth"}) || !slices.Equal(low.AcceptedIssuingCountries, []string{"NLD"}) {
		t.Errorf("low = %+v, %v; want the chip read added and the countries kept", low, err)
	}

	sub, err := withLevelNeeds(proofingprovider.FlowSpec{
		Name: "Substantial", RequiredAssuranceLevel: eidasSubstantial,
		Steps: []string{stepDocumentPhoto, stepFaceVerification}, FaceProvider: faceProviderIDScan,
		CheckThresholds: map[string]float64{checkFaceMatch: 0.6}, Apps: []string{appYivi},
	})
	if err != nil {
		t.Fatalf("substantial: %v", err)
	}
	if !slices.Equal(sub.Steps, []string{stepDocumentPhoto, stepFaceVerification, stepDocumentCapture, stepNFCRead}) ||
		!slices.Equal(sub.RequiredChecks, substantialChecks) || sub.FaceProvider != faceProviderRegula ||
		sub.CheckThresholds[checkFaceMatch] != substantialFaceMatchFloor || !slices.Contains(sub.Apps, appIdem) {
		t.Errorf("substantial = %+v; want the chip read, every check, Regula, the threshold at its floor and the Idem app", sub)
	}

	// The Yivi app reaches low: a Yivi-only flow on its personal data stays so.
	yivi, err := withLevelNeeds(proofingprovider.FlowSpec{
		Name: "DigiD", RequiredAssuranceLevel: eidasLow, Apps: []string{appYivi},
		AcceptedDocumentTypes: []string{"personal_data"},
	})
	if err != nil || !slices.Equal(yivi.Apps, []string{appYivi}) {
		t.Errorf("Yivi only at low = %+v, %v; want it kept to the Yivi app", yivi.Apps, err)
	}

	if _, err := withLevelNeeds(proofingprovider.FlowSpec{Name: "High", RequiredAssuranceLevel: flowLevelHigh}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("high = %v, want ErrInvalidInput", err)
	}
	if _, err := withLevelNeeds(proofingprovider.FlowSpec{Name: "X", RequiredAssuranceLevel: "medium"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("an unknown level = %v, want ErrInvalidInput", err)
	}
}
