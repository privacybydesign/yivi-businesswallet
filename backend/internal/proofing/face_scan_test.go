package proofing

import (
	"errors"
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
