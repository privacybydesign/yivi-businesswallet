package proofingengine

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// A Yivi result's face is released as is without a blur policy, blurred under
// one, and left out when it cannot be blurred.
func TestFaceFollowsRedaction(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := range 32 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	face := photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(buf.Bytes()), MimeType: "image/png"}

	if got, ok := releasedFace(face, privacy.RedactionPolicy{}); !ok || got.ImageBase64 != face.ImageBase64 {
		t.Errorf("without a policy: ok %v, changed %v; want the face as is", ok, got.ImageBase64 != face.ImageBase64)
	}
	blur := privacy.RedactionPolicy{BlurFace: true}
	if got, ok := releasedFace(face, blur); !ok || got.ImageBase64 == face.ImageBase64 {
		t.Errorf("under BlurFace: ok %v; want a blurred face", ok)
	}
	if _, ok := releasedFace(photoInfo{ImageBase64: "not an image", MimeType: "image/png"}, blur); ok {
		t.Error("an unblurrable face is released; want it left out")
	}
}
