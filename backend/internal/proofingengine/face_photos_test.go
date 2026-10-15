package proofingengine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// keepFaces is the policy of a flow that keeps its face photos.
var keepFaces = privacy.RedactionPolicy{KeepFacePhotos: true}

// faceKeys are the result keys that carry a face photo.
var faceKeys = []string{"photo", "selfie", "referencePhoto"}

func testFace(t *testing.T) photoInfo {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(buf.Bytes()), MimeType: "image/png"}
}

// checkFaces reports a face key the result has but should not, or lacks but
// should have.
func checkFaces(t *testing.T, result map[string]any, keys []string, want bool) {
	t.Helper()
	for _, key := range keys {
		if _, ok := result[key]; ok != want {
			t.Errorf("result has %s = %v, want %v", key, ok, want)
		}
	}
}

// The face photos are released only when the flow keeps them; the face
// match's verdicts are released either way.
func TestBuildResultReleasesFacesOnlyWhenKept(t *testing.T) {
	face := testFace(t)
	sess := session.Session{ReferencePhoto: face.ImageBase64, ReferencePhotoMime: face.MimeType}
	verified := true
	req := appResultRequest{Photo: &face, Selfie: &face, Biometrics: &biometricsInfo{FaceVerified: &verified}}

	byDefault := buildResult(sess, req, nil, nil, "", privacy.RedactionPolicy{})
	checkFaces(t, byDefault, faceKeys, false)
	if _, ok := byDefault["biometrics"]; !ok {
		t.Error("result lacks biometrics, want the face match's verdict kept")
	}
	if byDefault["faceReference"] != faceReferenceRelyingParty {
		t.Errorf("faceReference = %v, want %q", byDefault["faceReference"], faceReferenceRelyingParty)
	}

	checkFaces(t, buildResult(sess, req, nil, nil, "", keepFaces), faceKeys, true)
}

// The Yivi method follows the same rule for the disclosed photo and the last
// matching frame.
func TestBoundLoginResultReleasesFacesOnlyWhenKept(t *testing.T) {
	face := testFace(t)
	st := &session.YiviState{
		Reference: face.ImageBase64, ReferenceMime: face.MimeType, Disclosure: json.RawMessage(`{}`),
		KeepPhoto: true, LastFrame: face.ImageBase64,
	}
	s := &Server{}
	byDefault, err := s.buildBoundLoginResult(session.Session{}, st, privacy.RedactionPolicy{}, regula.DefaultMatchThreshold)
	if err != nil {
		t.Fatal(err)
	}
	checkFaces(t, byDefault, []string{"photo", "selfie"}, false)
	kept, err := s.buildBoundLoginResult(session.Session{}, st, keepFaces, regula.DefaultMatchThreshold)
	if err != nil {
		t.Fatal(err)
	}
	checkFaces(t, kept, []string{"photo", "selfie"}, true)
}

// A finished session's stored evidence keeps no face photo by default: the
// selfie's picture and the chip photo go, the selfie's verdicts stay.
func TestRedactStepsDropsFacePhotosByDefault(t *testing.T) {
	face := testFace(t)
	score := 0.9
	for name, tc := range map[string]struct {
		redaction privacy.RedactionPolicy
		kept      bool
	}{"by default": {privacy.RedactionPolicy{}, false}, "kept": {keepFaces, true}} {
		t.Run(name, func(t *testing.T) {
			raw, err := marshalToMap(submitNFCStepRequest{Photo: &face, MrtdEvidence: &mrtdEvidenceRequest{DataGroups: map[string]string{}}})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			sess := session.Session{Steps: session.StepEvidence{
				Selfie: &session.SelfieStepEvidence{Image: face.ImageBase64, MimeType: face.MimeType, LivenessPassed: true, FaceMatchScore: &score},
				NFC:    &session.NFCStepEvidence{Raw: raw},
			}}
			redactStepsForStorage(&sess, privacy.BSNPolicyRetrieve, tc.redaction)

			if got := sess.Steps.Selfie.Image != ""; got != tc.kept {
				t.Errorf("selfie picture kept = %v, want %v", got, tc.kept)
			}
			if !sess.Steps.Selfie.LivenessPassed || sess.Steps.Selfie.FaceMatchScore == nil {
				t.Error("the selfie's verdicts went with its picture, want them kept")
			}
			stored, err := decodeNFCStepRequest(sess.Steps.NFC.Raw)
			if err != nil {
				t.Fatalf("decode stored: %v", err)
			}
			if got := stored.Photo != nil; got != tc.kept {
				t.Errorf("chip photo kept = %v, want %v", got, tc.kept)
			}
		})
	}
}
