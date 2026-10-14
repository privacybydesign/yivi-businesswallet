package images

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"testing"
	"time"

	jpeg2000 "github.com/mrjoshuak/go-jpeg2000"
)

// jp2SamplePhotoBase64 loads testdata/passport_photo.jp2: a real ICAO 9303
// DG2 portrait, JPEG2000-encoded (the raw image bytes only, no DG2 TLV
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

func TestDisplayablePNGJPEG2000(t *testing.T) {
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
	// The source is a 449x599 portrait: a converter that produced a blank
	// or wildly wrong-sized image (e.g. from silently falling through to a
	// broken decode path) would fail this rather than the format check.
	if bounds.Dx() != 449 || bounds.Dy() != 599 {
		t.Errorf("decoded PNG dimensions = %dx%d, want 449x599", bounds.Dx(), bounds.Dy())
	}
}

func TestDisplayablePNGJPEG(t *testing.T) {
	// A minimal 1x1 white JPEG. Driving-licence DG6 portraits are typically
	// plain JPEG already and must not be touched: no browser rendering
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

func TestDisplayablePNGEmptyPhoto(t *testing.T) {
	gotBase64, gotMime, err := ToDisplayablePNG("", "")
	if err != nil {
		t.Fatalf("ToDisplayablePNG returned error: %v", err)
	}
	if gotBase64 != "" || gotMime != "" {
		t.Errorf("got (%q, %q), want (\"\", \"\")", gotBase64, gotMime)
	}
}

func TestDisplayablePNGBadBase64(t *testing.T) {
	if _, _, err := ToDisplayablePNG("not-actually-base64!!!", "image/jp2"); err == nil {
		t.Error("expected an error for invalid base64 input, got nil")
	}
}

// unused is a Decode use that fails the test when it runs.
func unused(t *testing.T) func(image.Image, string) error {
	t.Helper()
	return func(image.Image, string) error {
		t.Error("use ran for an image Decode refused")
		return nil
	}
}

// A JPEG2000 whose header claims more than MaxJPEG2000Pixels is refused
// before its raster is decoded, and passed through unconverted.
func TestDecodeRefusesHuge(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg2000.Encode(&buf, image.NewGray(image.Rect(0, 0, 2001, 2000)), nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := Decode(buf.Bytes(), unused(t)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Decode(a %d-byte JPEG2000 of 2001x2000) = %v, want ErrTooLarge", buf.Len(), err)
	}
	in := base64.StdEncoding.EncodeToString(buf.Bytes())
	if got, mime, err := ToDisplayablePNG(in, "image/jp2"); err != nil || got != in || mime != "image/jp2" {
		t.Errorf("ToDisplayablePNG = %d bytes, %q, %v; want the original back", len(got), mime, err)
	}
}

// manyComponents is a component count far past maxJPEG2000Components.
const manyComponents = 16384

// withComponents re-heads a raw J2K codestream (SOC, SIZ, ...) with a SIZ
// segment naming components colour planes: what the decoder sizes its
// buffers on.
func withComponents(t *testing.T, codestream []byte, components uint16) []byte {
	t.Helper()
	const sizStart = 2 // after SOC
	sizLen := int(binary.BigEndian.Uint16(codestream[sizStart+2:]))
	siz := codestream[sizStart : sizStart+2+sizLen]
	const fixedSIZ = 2 + 2 + 2 + 8*4 // marker, Lsiz, Rsiz, the eight sizes
	var b bytes.Buffer
	b.Write(codestream[:sizStart])
	b.Write(siz[:2])
	_ = binary.Write(&b, binary.BigEndian, uint16(fixedSIZ-2+2+3*int(components)))
	b.Write(siz[4:fixedSIZ])
	_ = binary.Write(&b, binary.BigEndian, components)
	component := siz[fixedSIZ+2 : fixedSIZ+5]
	for range components {
		b.Write(component)
	}
	b.Write(codestream[sizStart+2+sizLen:])
	return b.Bytes()
}

// A small JPEG2000 whose header names thousands of components is refused:
// the decoder would allocate a plane for each.
func TestDecodeRefusesManyComponents(t *testing.T) {
	var buf bytes.Buffer
	opts := jpeg2000.DefaultOptions()
	opts.Format = jpeg2000.FormatJ2K
	if err := jpeg2000.Encode(&buf, image.NewGray(image.Rect(0, 0, 16, 16)), opts); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := Decode(withComponents(t, buf.Bytes(), manyComponents), unused(t)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Decode(%d components) = %v, want ErrTooLarge", manyComponents, err)
	}
}

// Decode holds its slot until use returns, so what use derives from the
// raster (a copy, an encode) counts against maxConcurrentDecodes too.
func TestDecodeHoldsSlotThroughUse(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(jp2SamplePhotoBase64(t))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	errUse := errors.New("use failed")
	for name, result := range map[string]error{"success": nil, "failure": errUse} {
		t.Run(name, func(t *testing.T) {
			err := Decode(raw, func(image.Image, string) error {
				if held := len(decodeSlots); held != 1 {
					t.Errorf("slots held during use = %d, want 1", held)
				}
				return result
			})
			if !errors.Is(err, result) {
				t.Errorf("Decode = %v, want %v", err, result)
			}
			if held := len(decodeSlots); held != 0 {
				t.Errorf("slots held after Decode = %d, want 0", held)
			}
		})
	}
}

// shortLimit stands in for decodeWait and decodeTimeout, so a test of them
// runs in milliseconds.
const shortLimit = 50 * time.Millisecond

// withDecoder swaps the decoder and both limits for one test.
func withDecoder(t *testing.T, decode func(io.Reader) (image.Image, string, error)) {
	t.Helper()
	oldDecode, oldWait, oldTimeout := decodeImage, decodeWait, decodeTimeout
	decodeImage, decodeWait, decodeTimeout = decode, shortLimit, shortLimit
	t.Cleanup(func() { decodeImage, decodeWait, decodeTimeout = oldDecode, oldWait, oldTimeout })
}

func samplePhoto(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(jp2SamplePhotoBase64(t))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return raw
}

// waitForSlots polls until held slots are taken: a given-up decode frees its
// slot from its own goroutine.
func waitForSlots(t *testing.T, held int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(decodeSlots) != held {
		if time.Now().After(deadline) {
			t.Fatalf("slots held = %d, want %d", len(decodeSlots), held)
		}
		time.Sleep(time.Millisecond)
	}
}

// A decode that hangs costs the request decodeTimeout, and keeps its slot
// until it ends, so hung decodes never exceed maxConcurrentDecodes.
func TestDecodeGivesUpOnAHang(t *testing.T) {
	release := make(chan struct{})
	withDecoder(t, func(r io.Reader) (image.Image, string, error) {
		<-release
		return image.Decode(r)
	})
	if err := Decode(samplePhoto(t), unused(t)); !errors.Is(err, ErrDecodeTimeout) {
		t.Fatalf("Decode = %v, want ErrDecodeTimeout", err)
	}
	if held := len(decodeSlots); held != 1 {
		t.Errorf("slots held while the decode hangs = %d, want 1", held)
	}
	close(release)
	waitForSlots(t, 0)
}

// With every slot taken for decodeWait, a decode fails fast instead of
// queueing forever.
func TestDecodeBusyWhenEverySlotIsTaken(t *testing.T) {
	withDecoder(t, image.Decode)
	for range maxConcurrentDecodes {
		decodeSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range maxConcurrentDecodes {
			<-decodeSlots
		}
	})
	if err := Decode(samplePhoto(t), unused(t)); !errors.Is(err, ErrBusy) {
		t.Errorf("Decode = %v, want ErrBusy", err)
	}
}

// A decoder that panics on an image fails that image and frees its slot:
// the decode runs on a goroutine nothing else would recover.
func TestDecodeRecoversADecoderPanic(t *testing.T) {
	withDecoder(t, func(io.Reader) (image.Image, string, error) {
		panic("index out of range")
	})
	if err := Decode(samplePhoto(t), unused(t)); !errors.Is(err, ErrDecoderPanic) {
		t.Errorf("Decode = %v, want ErrDecoderPanic", err)
	}
	waitForSlots(t, 0)
}
