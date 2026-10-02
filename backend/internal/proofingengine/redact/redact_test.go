package redact

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// checkerboardPNG returns a 64x64 base64 PNG with a high-frequency
// checkerboard pattern (alternating black/white pixels) so a redacted
// region is easy to distinguish from an untouched one: pixelation flattens
// the checkerboard's per-pixel variance within the redacted block, while an
// untouched region keeps it.
func checkerboardPNG(t *testing.T, size int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			c := color.RGBA{A: 255}
			if (x+y)%2 == 0 {
				c.R, c.G, c.B = 255, 255, 255
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

func decodePNG(t *testing.T, imageBase64 string) *image.RGBA {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return toRGBA(img)
}

// variance reports whether the pixel block has more than one distinct
// colour — used as a proxy for "still shows the original checkerboard".
func hasVariance(img *image.RGBA, r image.Rectangle) bool {
	first := img.RGBAAt(r.Min.X, r.Min.Y)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBAAt(x, y) != first {
				return true
			}
		}
	}
	return false
}

func TestImageBlursWholeImage(t *testing.T) {
	src := checkerboardPNG(t, 64)
	out, mime, err := Image(src, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	got := decodePNG(t, out)
	if hasVariance(got, got.Bounds()) {
		t.Error("whole image still shows checkerboard variance after Image()")
	}
}

func TestRegionOnlyBlursTheGivenRect(t *testing.T) {
	src := checkerboardPNG(t, 64)
	// Redact only the left half.
	out, _, err := Region(src, "image/png", Rect{X: 0, Y: 0, W: 0.5, H: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := decodePNG(t, out)

	left := image.Rect(0, 0, 32, 64)
	right := image.Rect(32, 0, 64, 64)
	if hasVariance(got, left) {
		t.Error("left half (the redacted region) still shows checkerboard variance")
	}
	if !hasVariance(got, right) {
		t.Error("right half (outside the region) was unexpectedly redacted")
	}
}

func TestRegionClampsOutOfRangeCoordinates(t *testing.T) {
	src := checkerboardPNG(t, 32)
	if _, _, err := Region(src, "image/png", Rect{X: -0.5, Y: -0.5, W: 2, H: 2}); err != nil {
		t.Fatalf("out-of-range rect should clamp, not error: %v", err)
	}
}

func TestImageEmptyInputIsPassthrough(t *testing.T) {
	out, mime, err := Image("", "image/jpeg")
	if err != nil || out != "" || mime != "image/jpeg" {
		t.Fatalf("Image(\"\", ...) = %q, %q, %v, want passthrough", out, mime, err)
	}
}

func TestImagePreservesJPEGFormat(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	src := base64.StdEncoding.EncodeToString(buf.Bytes())

	_, mime, err := Image(src, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg (format should be preserved)", mime)
	}
}

func TestImageRejectsCorruptData(t *testing.T) {
	if _, _, err := Image("not-actually-base64-image-data", "image/jpeg"); err == nil {
		t.Fatal("expected an error for corrupt/undecodable image data")
	}
}
