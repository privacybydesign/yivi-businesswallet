package redact

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
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

// coveredFace checks that out is a PNG of width x height, all faceColor.
func coveredFace(t *testing.T, out, mime string, width, height int) {
	t.Helper()
	if mime != mimePNG {
		t.Errorf("mime = %q, want %s", mime, mimePNG)
	}
	got := decodePNG(t, out)
	if got.Bounds() != image.Rect(0, 0, width, height) {
		t.Errorf("covered image is %v, want %dx%d", got.Bounds(), width, height)
	}
	if hasVariance(got, got.Bounds()) || got.RGBAAt(0, 0) != faceColor {
		t.Errorf("covered image is not all %v", faceColor)
	}
}

// Nothing of a face is kept: a mosaic or blur can still be matched against
// candidate photos.
func TestImageCoversWholeImage(t *testing.T) {
	out, mime, err := Image(checkerboardPNG(t, 64), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	coveredFace(t, out, mime, 64, 64)
}

// A chip's JPEG2000 portrait is covered from its header alone.
func TestImageCoversAJPEG2000Portrait(t *testing.T) {
	raw, err := os.ReadFile("../images/testdata/passport_photo.jp2")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := images.Config(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, mime, err := Image(base64.StdEncoding.EncodeToString(raw), "image/jp2")
	if err != nil {
		t.Fatal(err)
	}
	coveredFace(t, out, mime, cfg.Width, cfg.Height)
}

// The header's size is held to the decode limits, so a covered image is never
// larger than an upload may be.
func TestImageRefusesAnOversizedHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, images.MaxImagePixels/1000+1, 1000))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Image(base64.StdEncoding.EncodeToString(buf.Bytes()), "image/png"); !errors.Is(err, images.ErrTooLarge) {
		t.Errorf("Image = %v, want ErrTooLarge", err)
	}
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

func TestImageEmptyPassesThrough(t *testing.T) {
	out, mime, err := Image("", "image/jpeg")
	if err != nil || out != "" || mime != "image/jpeg" {
		t.Fatalf("Image(\"\", ...) = %q, %q, %v, want passthrough", out, mime, err)
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

func TestImageRejectsCorruptData(t *testing.T) {
	if _, _, err := Image("not-actually-base64-image-data", "image/jpeg"); err == nil {
		t.Fatal("expected an error for corrupt/undecodable image data")
	}
}
