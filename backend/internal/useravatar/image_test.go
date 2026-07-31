package useravatar

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// pngBytes encodes an opaque square PNG of the given size.
func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// jpegWithComment encodes a square JPEG carrying a COM segment, standing in for
// the metadata (EXIF and friends) a camera photo arrives with.
func jpegWithComment(t *testing.T, size int, comment string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: 0x20, G: uint8(x), B: uint8(y), A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	raw := buf.Bytes()

	// Splice a COM marker (0xFFFE, length-prefixed) in right after the SOI so the
	// bytes handed to normalize really do carry metadata.
	const soiLen = 2
	segment := []byte{0xFF, 0xFE, 0x00, byte(len(comment) + 2)}
	out := make([]byte, 0, len(raw)+len(segment)+len(comment))
	out = append(out, raw[:soiLen]...)
	out = append(out, segment...)
	out = append(out, comment...)
	out = append(out, raw[soiLen:]...)
	return out
}

func TestNormalizeAcceptsPNG(t *testing.T) {
	got, err := normalize(pngBytes(t, 64))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", got.ContentType)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(got.Bytes)); err != nil || format != formatPNG {
		t.Errorf("stored bytes decode as (%q, %v), want a png", format, err)
	}
}

// The re-encode is the EXIF/metadata strip the privacy posture depends on: a
// photo off a phone must not keep the location it was taken at.
func TestNormalizeStripsJPEGMetadata(t *testing.T) {
	const secret = "GPS 52.0907,5.1214"
	data := jpegWithComment(t, 64, secret)
	if !bytes.Contains(data, []byte(secret)) {
		t.Fatal("test fixture does not carry the comment it is meant to")
	}

	got, err := normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q, want image/jpeg", got.ContentType)
	}
	if bytes.Contains(got.Bytes, []byte(secret)) {
		t.Error("stored bytes still contain the original metadata comment")
	}
}

func TestNormalizeRejectsNonImage(t *testing.T) {
	_, err := normalize([]byte("this is plainly not an image file"))
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}

// GIF, WebP and SVG are fine for an org logo but not for an avatar: only the two
// formats the stdlib can re-encode (and so strip metadata from) are accepted.
func TestNormalizeRejectsOtherImageFormats(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black, color.White})
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}

	_, err := normalize(buf.Bytes())
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError for a GIF", err)
	}
}

// The dimension check reads the header only, so an oversized image is refused
// before a pixel buffer is allocated for it.
func TestNormalizeRejectsOversizedDimensions(t *testing.T) {
	_, err := normalize(pngBytes(t, MaxAvatarDimension+1))
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}

func TestNormalizeRejectsWhatReEncodesTooLarge(t *testing.T) {
	// A full-size noise PNG does not compress, so the re-encoded result exceeds
	// MaxAvatarBytes even though the input was accepted by the byte-cap check.
	img := image.NewRGBA(image.Rect(0, 0, MaxAvatarDimension, MaxAvatarDimension))
	seed := uint32(1)
	for y := range MaxAvatarDimension {
		for x := range MaxAvatarDimension {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{R: uint8(seed >> 16), G: uint8(seed >> 8), B: uint8(seed), A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	_, err := normalize(buf.Bytes())
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %v, want 413 APIError", err)
	}
}
