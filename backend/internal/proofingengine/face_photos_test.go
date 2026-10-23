package proofingengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
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

// The Yivi method follows the same rule for the disclosed photo.
func TestBoundLoginResultReleasesPhotoOnlyWhenKept(t *testing.T) {
	face := testFace(t)
	s := &Server{}
	for name, tc := range map[string]struct {
		redaction privacy.RedactionPolicy
		requested []string
		kept      bool
	}{
		"by default":      {privacy.RedactionPolicy{}, nil, false},
		"kept":            {keepFaces, nil, true},
		"kept, not asked": {keepFaces, []string{attrDocument}, false},
	} {
		sess := session.Session{RequestedAttributes: tc.requested}
		result := buildBoundLoginResult(sess, disclosureInfo{}, s.disclosedPhoto(sess, face.ImageBase64, tc.redaction), nil)
		if _, got := result["photo"]; got != tc.kept {
			t.Errorf("%s: photo released = %v, want %v", name, got, tc.kept)
		}
		if _, ok := result["selfie"]; ok {
			t.Errorf("%s: a Yivi result carries a selfie", name)
		}
	}
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

// A session that waits for review keeps its face photos in the result and the
// stored evidence whatever its flow says, so the reviewer can compare them; a
// decided one follows the flow.
func TestHeldForReviewKeepsFaces(t *testing.T) {
	face := testFace(t)
	sess := session.Session{ReferencePhoto: face.ImageBase64, ReferencePhotoMime: face.MimeType}
	req := appResultRequest{Photo: &face, Selfie: &face}
	for status, want := range map[session.Status]bool{
		session.StatusNeedsReview: true, session.StatusApproved: false, session.StatusRejected: false,
	} {
		redaction := heldForReview(status, privacy.RedactionPolicy{})
		if redaction.KeepFacePhotos != want {
			t.Errorf("%s: keeps face photos = %v, want %v", status, redaction.KeepFacePhotos, want)
		}
		checkFaces(t, buildResult(sess, req, nil, nil, "", redaction), faceKeys, want)
	}
	if !heldForReview(session.StatusApproved, keepFaces).KeepFacePhotos {
		t.Error("a flow that keeps its face photos lost them on a decided session")
	}
}

// Deciding a review drops the face photos the session held for it, from the
// result and the stored evidence, unless the flow keeps them; the verdicts stay.
func TestDecideReviewDropsHeldFaces(t *testing.T) {
	face := testFace(t)
	score := 0.93
	kept := true
	for name, tc := range map[string]struct {
		keep *bool
		want bool
	}{"by default": {nil, false}, "kept": {&kept, true}} {
		t.Run(name, func(t *testing.T) {
			sess := inProgress("ps_1")
			sess.Status, sess.Flow, sess.FlowVersion = session.StatusNeedsReview, "flow", 1
			sess.Result = map[string]any{
				"photo": &face, "selfie": &face, "referencePhoto": &face,
				"biometrics": &biometricsInfo{FaceMatchScore: &score},
			}
			sess.Steps.Selfie = &session.SelfieStepEvidence{Image: face.ImageBase64, MimeType: face.MimeType, FaceMatchScore: &score}
			store := newMemSessions(sess)
			fd := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Payslip", KeepFacePhotos: tc.keep}
			s := New(DefaultConfig(), store, singleFlow{fd: fd}, nil, nil)

			if err := s.DecideReview(context.Background(), pp.Tenant{ID: "org"}, sess.ID, sess.Token,
				pp.ReviewDecision{Reason: "Not the person's payslip", Reviewer: "Anna Jansen"}); err != nil {
				t.Fatalf("DecideReview: %v", err)
			}
			stored, _ := store.Get("org", sess.ID)
			checkFaces(t, stored.Result, faceKeys, tc.want)
			if _, ok := stored.Result["biometrics"]; !ok {
				t.Error("the face match's verdict went with the photos, want it kept")
			}
			if got := stored.Steps.Selfie.Image != ""; got != tc.want {
				t.Errorf("stored selfie kept = %v, want %v", got, tc.want)
			}
			if stored.Steps.Selfie.FaceMatchScore == nil {
				t.Error("the stored selfie's score went with its picture, want it kept")
			}
		})
	}
}
