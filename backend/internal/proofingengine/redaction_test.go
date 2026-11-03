package proofingengine

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"maps"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// Under BlurBSN a document image whose BSN cannot be covered (here: bytes
// Go cannot decode) is withheld from the result, never released readable.
func TestReleasedImageFailsClosed(t *testing.T) {
	undecodable := &documentImageInfo{
		ImageBase64: base64.StdEncoding.EncodeToString([]byte("not an image")), MimeType: "image/heic",
		BSNRegion: &imageRegion{X: 0.1, Y: 0.1, W: 0.2, H: 0.05},
	}
	if got := releasedDocumentImage(session.Session{}, undecodable, privacy.RedactionPolicy{BlurBSN: true}); got != nil {
		t.Errorf("released %q under BlurBSN, want it withheld", got.MimeType)
	}
	if got := releasedDocumentImage(session.Session{}, undecodable, privacy.RedactionPolicy{}); got == nil {
		t.Error("withheld without a BlurBSN policy, want it released as sent")
	}
	if got := releasedDocumentImage(session.Session{}, nil, privacy.RedactionPolicy{BlurBSN: true}); got != nil {
		t.Error("released an image that was never sent")
	}
}

// Under BlurBSN a document photo without a located BSN region is withheld,
// from the result and from storage: the server never guesses where the BSN
// is, so it never releases or keeps the photo readable.
func TestUnlocatedBSNWithheld(t *testing.T) {
	photo := checkerboardPNG(t)
	policy := privacy.RedactionPolicy{BlurBSN: true}

	if got := releasedDocumentImage(session.Session{}, &documentImageInfo{ImageBase64: photo, MimeType: "image/png"}, policy); got != nil {
		t.Error("released a document photo under BlurBSN without a region, want it withheld")
	}

	located := &documentImageInfo{ImageBase64: photo, MimeType: "image/png", BSNRegion: &imageRegion{X: 0.1, Y: 0.1, W: 0.2, H: 0.05}}
	if got := releasedDocumentImage(session.Session{}, located, policy); got == nil || got.ImageBase64 == photo {
		t.Error("a photo with a located BSN was not released covered")
	}

	sess := session.Session{Steps: session.StepEvidence{DocumentPhoto: &session.DocumentPhotoStepEvidence{
		Front: session.DocumentPhotoSide{Image: photo, MimeType: "image/png"},
	}}}
	redactStepsForStorage(&sess, privacy.BSNPolicyRetrieve, policy)
	if stored := sess.Steps.DocumentPhoto.Front.Image; stored != "" {
		t.Error("stored a document photo under BlurBSN without a region, want it dropped")
	}
}

// Black out the BSN covers only a document that may print one: a photo of a
// document known not to be Dutch is released and stored as taken, though the
// app located no BSN on it. A Dutch document, or one whose issuing state is
// not known, without a located BSN is still withheld.
func TestBlurBSNOnlyDutchDocuments(t *testing.T) {
	photo := checkerboardPNG(t)
	policy := privacy.RedactionPolicy{BlurBSN: true}
	cases := []struct {
		name     string
		state    string
		released bool
	}{
		{"Belgian", "BEL", true},
		{"German", "D", true},
		{"Dutch", dutchIssuingState, false},
		{"issuing state not known", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := appResultRequest{
				Document:      &documentInfo{IssuingState: tc.state},
				DocumentImage: &documentImageInfo{ImageBase64: photo, MimeType: "image/png"},
			}
			img, _ := buildResult(session.Session{}, req, nil, nil, privacy.BSNPolicyRetrieve, policy)["documentImage"].(*documentImageInfo)
			if released := img != nil; released != tc.released {
				t.Errorf("document photo released = %v, want %v", released, tc.released)
			}
			if img != nil && img.ImageBase64 != photo {
				t.Error("a document photo with no BSN to cover was changed")
			}

			sess := session.Session{Steps: session.StepEvidence{
				Document: &session.DocumentStepEvidence{Parsed: session.ParsedMRZ{IssuingState: tc.state}},
				DocumentPhoto: &session.DocumentPhotoStepEvidence{
					Front: session.DocumentPhotoSide{Image: photo, MimeType: "image/png"},
				},
			}}
			redactStepsForStorage(&sess, privacy.BSNPolicyRetrieve, policy)
			if stored := sess.Steps.DocumentPhoto.Front.Image; (stored == photo) != tc.released {
				t.Errorf("document photo stored as taken = %v, want %v", stored == photo, tc.released)
			}
		})
	}
}

// checkerboardPNG is a base64 PNG whose every pixel differs from its
// neighbours, so any pixelation changes it.
func checkerboardPNG(t *testing.T) string {
	t.Helper()
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			c := color.RGBA{A: 0xff}
			if (x+y)%2 == 0 {
				c.R, c.G, c.B = 0xff, 0xff, 0xff
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// Once a session is finished, the raw chip data groups that are the face and
// the BSN again go with the policy: DG2 unless the flow keeps face photos, a
// Dutch document's DG1 and DG11 under mask or omit. DG15 (the AA key) stays.
func TestRedactStepsDropsRawDGs(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		bsn       privacy.BSNPolicy
		redaction privacy.RedactionPolicy
		want      []string
	}{
		{"retrieve, face photos kept", dutchIssuingState, privacy.BSNPolicyRetrieve, keepFaces, []string{"DG1", "DG11", "DG15", "DG2"}},
		{"retrieve, by default", dutchIssuingState, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{}, []string{"DG1", "DG11", "DG15"}},
		{"omit, Dutch", dutchIssuingState, privacy.BSNPolicyOmit, keepFaces, []string{"DG15", "DG2"}},
		{"mask, Dutch", dutchIssuingState, privacy.BSNPolicyMask, keepFaces, []string{"DG15", "DG2"}},
		{"omit, German", "D", privacy.BSNPolicyOmit, keepFaces, []string{"DG1", "DG11", "DG15", "DG2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := marshalToMap(submitNFCStepRequest{
				Document: &documentInfo{IssuingState: tc.state},
				MrtdEvidence: &mrtdEvidenceRequest{DataGroups: map[string]string{
					"DG1": "61", "DG2": "75", "DG11": "6b", "DG15": "6f",
				}},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			sess := session.Session{Steps: session.StepEvidence{NFC: &session.NFCStepEvidence{Raw: raw}}}
			redactStepsForStorage(&sess, tc.bsn, tc.redaction)
			stored, err := decodeNFCStepRequest(sess.Steps.NFC.Raw)
			if err != nil {
				t.Fatalf("decode stored: %v", err)
			}
			got := slices.Sorted(maps.Keys(stored.MrtdEvidence.DataGroups))
			if !slices.Equal(got, tc.want) {
				t.Errorf("stored data groups = %v, want %v", got, tc.want)
			}
		})
	}
}

// Only the flow's "Black out the BSN" (BlurBSN) covers the BSN on a Dutch (or
// unknown) document's photos: covered where it was located, withheld where it
// was not. A mask or omit BSN policy is about the BSN as data and leaves the
// photo as taken, as it does another country's document photo.
func TestBlurBSNAloneCoversDocumentPhoto(t *testing.T) {
	photo := checkerboardPNG(t)
	region := &imageRegion{X: 0.1, Y: 0.1, W: 0.2, H: 0.05}
	blur := privacy.RedactionPolicy{BlurBSN: true}
	req := appResultRequest{
		Document:          &documentInfo{IssuingState: dutchIssuingState},
		DocumentImage:     &documentImageInfo{ImageBase64: photo, MimeType: "image/png", BSNRegion: region},
		DocumentImageBack: &documentImageInfo{ImageBase64: photo, MimeType: "image/png"},
	}
	result := buildResult(session.Session{}, req, nil, nil, privacy.BSNPolicyOmit, blur)
	if img, _ := result["documentImage"].(*documentImageInfo); img == nil || img.ImageBase64 == photo {
		t.Error("a located BSN was not covered under BlurBSN")
	}
	if _, ok := result["documentImageBack"]; ok {
		t.Error("released a photo whose BSN was not located under BlurBSN, want it withheld")
	}

	for _, policy := range []privacy.BSNPolicy{privacy.BSNPolicyMask, privacy.BSNPolicyOmit} {
		if img, _ := buildResult(session.Session{}, req, nil, nil, policy, privacy.RedactionPolicy{})["documentImageBack"].(*documentImageInfo); img == nil || img.ImageBase64 != photo {
			t.Errorf("BSN policy %s without BlurBSN changed the photo, want it as taken", policy)
		}
	}

	req.Document.IssuingState = "D"
	if img, _ := buildResult(session.Session{}, req, nil, nil, privacy.BSNPolicyOmit, blur)["documentImageBack"].(*documentImageInfo); img == nil || img.ImageBase64 != photo {
		t.Error("a German document photo was not released as taken")
	}

	sess := session.Session{Steps: session.StepEvidence{DocumentPhoto: &session.DocumentPhotoStepEvidence{
		Front: session.DocumentPhotoSide{Image: photo, MimeType: "image/png"},
	}}}
	redactStepsForStorage(&sess, privacy.BSNPolicyOmit, blur)
	if stored := sess.Steps.DocumentPhoto.Front.Image; stored != "" {
		t.Error("stored a document photo whose BSN was not located under BlurBSN, want it dropped")
	}
}
