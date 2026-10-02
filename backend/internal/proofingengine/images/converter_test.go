package images

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"testing"
)

// jp2SamplePhotoBase64 loads testdata/passport_photo.jp2: a real ICAO 9303
// DG2 portrait, JPEG2000-encoded (the raw image bytes only — no DG2 TLV
// wrapper), extracted from the same sample go-passport-issuer's images
// package tests against. This is what the vcmrtd app sends as
// photo.imageBase64 with mimeType "image/jp2".
func jp2SamplePhotoBase64(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/passport_photo.jp2")
	if err != nil {
		t.Fatalf("failed to read testdata/passport_photo.jp2: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestToDisplayablePNG_JPEG2000IsConvertedToPNG(t *testing.T) {
	gotBase64, gotMime, err := ToDisplayablePNG(jp2SamplePhotoBase64(t), "image/jp2")
	if err != nil {
		t.Fatalf("ToDisplayablePNG returned error: %v", err)
	}
	if gotMime != "image/png" {
		t.Errorf("mimeType = %q, want %q", gotMime, "image/png")
	}

	raw, err := base64.StdEncoding.DecodeString(gotBase64)
	if err != nil {
		t.Fatalf("output is not valid base64: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a valid PNG: %v", err)
	}
	bounds := img.Bounds()
	// The source is a 449x599 portrait — a converter that produced a blank
	// or wildly wrong-sized image (e.g. from silently falling through to a
	// broken decode path) would fail this rather than the format check.
	if bounds.Dx() != 449 || bounds.Dy() != 599 {
		t.Errorf("decoded PNG dimensions = %dx%d, want 449x599", bounds.Dx(), bounds.Dy())
	}
}

func TestToDisplayablePNG_JPEGPassesThroughUnchanged(t *testing.T) {
	// A minimal 1x1 white JPEG. Driving-licence DG6 portraits are typically
	// plain JPEG already and must not be touched — no browser rendering
	// problem to solve, and re-encoding would lose quality for no reason.
	const jpegBase64 = "/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8QEBD/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAAAAAACP/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AVN4A/9k="

	gotBase64, gotMime, err := ToDisplayablePNG(jpegBase64, "image/jpeg")
	if err != nil {
		t.Fatalf("ToDisplayablePNG returned error: %v", err)
	}
	if gotMime != "image/jpeg" {
		t.Errorf("mimeType = %q, want unchanged %q", gotMime, "image/jpeg")
	}
	if gotBase64 != jpegBase64 {
		t.Error("JPEG bytes were modified; expected pass-through unchanged")
	}
}

func TestToDisplayablePNG_EmptyPhotoPassesThrough(t *testing.T) {
	gotBase64, gotMime, err := ToDisplayablePNG("", "")
	if err != nil {
		t.Fatalf("ToDisplayablePNG returned error: %v", err)
	}
	if gotBase64 != "" || gotMime != "" {
		t.Errorf("got (%q, %q), want (\"\", \"\")", gotBase64, gotMime)
	}
}

func TestToDisplayablePNG_InvalidBase64Errors(t *testing.T) {
	if _, _, err := ToDisplayablePNG("not-actually-base64!!!", "image/jp2"); err == nil {
		t.Error("expected an error for invalid base64 input, got nil")
	}
}
