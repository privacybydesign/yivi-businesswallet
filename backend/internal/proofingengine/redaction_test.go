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
// the BSN again go with the policy: DG2 under BlurFace, a Dutch document's
// DG1 and DG11 under mask or omit. DG15 (the AA key) stays.
func TestRedactStepsDropsRawDGs(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		bsn       privacy.BSNPolicy
		redaction privacy.RedactionPolicy
		want      []string
	}{
		{"retrieve, no blur", dutchIssuingState, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{}, []string{"DG1", "DG11", "DG15", "DG2"}},
		{"blur face", dutchIssuingState, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{BlurFace: true}, []string{"DG1", "DG11", "DG15"}},
		{"omit, Dutch", dutchIssuingState, privacy.BSNPolicyOmit, privacy.RedactionPolicy{}, []string{"DG15", "DG2"}},
		{"mask, Dutch", dutchIssuingState, privacy.BSNPolicyMask, privacy.RedactionPolicy{}, []string{"DG15", "DG2"}},
		{"omit, German", "D", privacy.BSNPolicyOmit, privacy.RedactionPolicy{}, []string{"DG1", "DG11", "DG15", "DG2"}},
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
