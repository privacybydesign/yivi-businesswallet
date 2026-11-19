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

// checkerboardPNG returns a size x size base64 PNG with a high-frequency
// checkerboard pattern (alternating black/white pixels) so a redacted
// region is easy to distinguish from an untouched one: a cover flattens the
// checkerboard's per-pixel variance, while an untouched region keeps it.
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
// colour, used as a proxy for "still shows the original checkerboard".
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

func TestRegionBlursOnlyItsRect(t *testing.T) {
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
	// Covered, not pixelated: a mosaic of a printed number can be read back.
	if c := got.RGBAAt(0, 0); c != redactionColor {
		t.Errorf("redacted region = %v, want it covered in %v", c, redactionColor)
	}
	if !hasVariance(got, right) {
		t.Error("right half (outside the region) was unexpectedly redacted")
	}
}

func TestRegionClampsCoordinates(t *testing.T) {
	src := checkerboardPNG(t, 32)
	if _, _, err := Region(src, "image/png", Rect{X: -0.5, Y: -0.5, W: 2, H: 2}); err != nil {
		t.Fatalf("out-of-range rect should clamp, not error: %v", err)
	}
}

func TestRegionPreservesJPEGFormat(t *testing.T) {
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

	_, mime, err := Region(src, "image/jpeg", Rect{X: 0, Y: 0, W: 0.5, H: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if mime != mimeJPEG {
		t.Errorf("mime = %q, want %s (format should be preserved)", mime, mimeJPEG)
	}
}
