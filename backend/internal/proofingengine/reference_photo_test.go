package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A face matched against the relying party's photo says so in the result, and
// releases that photo with the selfie, so a reviewer sees both faces.
func TestResultCarriesTheReferencePhoto(t *testing.T) {
	sess := session.Session{
		ID: "s1", ReferencePhoto: "cGhvdG8=", ReferencePhotoMime: "image/png",
		RequestedAttributes: []string{attrSelfie, attrBiometrics},
	}
	result := buildResult(sess, appResultRequest{}, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})
	if result["faceReference"] != faceReferenceRelyingParty {
		t.Errorf("faceReference = %v, want %q", result["faceReference"], faceReferenceRelyingParty)
	}
	ref, ok := result["referencePhoto"].(*photoInfo)
	if !ok || ref.ImageBase64 != sess.ReferencePhoto || ref.MimeType != sess.ReferencePhotoMime {
		t.Errorf("referencePhoto = %v, want the relying party's photo", result["referencePhoto"])
	}

	sess.RequestedAttributes = []string{attrBiometrics}
	result = buildResult(sess, appResultRequest{}, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})
	if _, ok := result["referencePhoto"]; ok || result["faceReference"] != faceReferenceRelyingParty {
		t.Errorf("without the selfie requested: result = %v; want only the faceReference marker", result)
	}

	chip := session.Session{ID: "s2"}
	result = buildResult(chip, appResultRequest{}, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})
	if _, ok := result["faceReference"]; ok {
		t.Error("a chip session claims a relying-party reference")
	}
}
