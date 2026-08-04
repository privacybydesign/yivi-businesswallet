package user

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"testing"
	"time"
)

// pngOf encodes a solid-colour image of the given size, the simplest stand-in for
// an uploaded photo.
func pngOf(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// halvesPNG is a wide image whose left half is red and right half is blue, with a
// green band down the far left. A centre crop drops the green band; a squash of the
// whole frame would keep it, so the two are distinguishable in the output.
func halvesPNG(t *testing.T) []byte {
	t.Helper()
	const w, h = 400, 100
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			switch {
			case x < 20:
				img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
			case x < w/2:
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			default:
				img.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return img
}

func TestNormalizeAvatarProducesFixedSquareJPEG(t *testing.T) {
	avatar, err := NormalizeAvatar(pngOf(t, 640, 480, color.RGBA{R: 12, G: 78, B: 137, A: 255}))
	if err != nil {
		t.Fatalf("NormalizeAvatar: %v", err)
	}
	if avatar.ContentType != AvatarContentType {
		t.Errorf("content type = %q, want %q", avatar.ContentType, AvatarContentType)
	}

	got := decode(t, avatar.Bytes).Bounds()
	want := image.Rect(0, 0, AvatarSize, AvatarSize)
	if got != want {
		t.Errorf("bounds = %v, want %v", got, want)
	}
}

func TestNormalizeAvatarCropsToCentreSquare(t *testing.T) {
	avatar, err := NormalizeAvatar(halvesPNG(t))
	if err != nil {
		t.Fatalf("NormalizeAvatar: %v", err)
	}

	// The 100px-tall source crops to its centre 100x100, which sits astride the
	// red/blue seam: the left edge of the result must be red, not the green band
	// that only a full-width squash would have pulled in.
	r, g, b, _ := decode(t, avatar.Bytes).At(4, AvatarSize/2).RGBA()
	if r>>8 < 200 || g>>8 > 80 || b>>8 > 80 {
		t.Errorf("left edge = (%d,%d,%d), want red — the crop is not centred", r>>8, g>>8, b>>8)
	}
}

func TestNormalizeAvatarRejectsNonImage(t *testing.T) {
	if _, err := NormalizeAvatar([]byte("this is not an image")); !errors.Is(err, ErrAvatarUnsupported) {
		t.Errorf("error = %v, want ErrAvatarUnsupported", err)
	}
}

func TestNormalizeAvatarRejectsEmptyInput(t *testing.T) {
	if _, err := NormalizeAvatar(nil); !errors.Is(err, ErrAvatarUnsupported) {
		t.Errorf("error = %v, want ErrAvatarUnsupported", err)
	}
}

// TestNormalizeAvatarRejectsPixelBomb uses a hand-built PNG header that declares
// an enormous canvas: the guard must reject it from the header alone, without ever
// allocating the bitmap.
func TestNormalizeAvatarRejectsPixelBomb(t *testing.T) {
	if _, err := NormalizeAvatar(pngHeaderOnly(30000, 30000)); !errors.Is(err, ErrAvatarTooLarge) {
		t.Errorf("error = %v, want ErrAvatarTooLarge", err)
	}
}

// pngHeaderOnly is a PNG signature plus a valid IHDR chunk and nothing else —
// enough for image.DecodeConfig to report the declared dimensions.
func pngHeaderOnly(w, h uint32) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	_ = binary.Write(&ihdr, binary.BigEndian, w)
	_ = binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 6, 0, 0, 0}) // 8-bit RGBA, no interlace

	var out bytes.Buffer
	out.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()-len("IHDR")))
	out.Write(ihdr.Bytes())
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return out.Bytes()
}

// jpegWithEXIF wraps a JPEG's scan data in an APP1 Exif segment carrying a
// little-endian TIFF header with a single Orientation tag, plus a recognisable
// marker string standing in for the rest of the camera metadata.
func jpegWithEXIF(t *testing.T, base []byte, orientation uint16, marker string) []byte {
	t.Helper()

	var tiff bytes.Buffer
	tiff.Write([]byte{'I', 'I', 0x2A, 0x00})                     // little-endian, magic
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8))      // first IFD at offset 8
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1))      // one entry
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112)) // Orientation
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3))      // SHORT
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))      // one value
	_ = binary.Write(&tiff, binary.LittleEndian, orientation)    // value
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))      // value padding
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0))      // no next IFD
	tiff.WriteString(marker)

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := []byte{0xFF, 0xE1}
	segment = binary.BigEndian.AppendUint16(segment, uint16(len(payload)+2))
	segment = append(segment, payload...)

	// The APP1 segment goes straight after the SOI marker.
	out := append([]byte{}, base[:2]...)
	out = append(out, segment...)
	return append(out, base[2:]...)
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizeAvatarDropsEXIFMetadata(t *testing.T) {
	const marker = "GPS-LATITUDE-52.09"
	source := jpegWithEXIF(t, jpegOf(t, 300, 300), 1, marker)
	if !bytes.Contains(source, []byte(marker)) {
		t.Fatal("test fixture does not carry the marker; the EXIF block was not embedded")
	}

	avatar, err := NormalizeAvatar(source)
	if err != nil {
		t.Fatalf("NormalizeAvatar: %v", err)
	}
	if bytes.Contains(avatar.Bytes, []byte(marker)) {
		t.Error("the stored avatar still carries the source's EXIF metadata")
	}
}

func TestNormalizeAvatarAppliesEXIFOrientation(t *testing.T) {
	// A left-half-black, right-half-white square rotated 90° clockwise (the
	// orientation-6 transform) has its black half on top.
	const edge = 200
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for y := range edge {
		for x := range edge {
			if x < edge/2 {
				img.Set(x, y, color.Black)
			} else {
				img.Set(x, y, color.White)
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	avatar, err := NormalizeAvatar(jpegWithEXIF(t, buf.Bytes(), 6, ""))
	if err != nil {
		t.Fatalf("NormalizeAvatar: %v", err)
	}

	// Sampled well clear of the seam, in a column that is uniformly white in the
	// untransformed image — so a missing rotation fails this rather than landing on
	// an ambiguous edge pixel.
	const column = AvatarSize * 3 / 4
	out := decode(t, avatar.Bytes)
	top, _, _, _ := out.At(column, 10).RGBA()
	bottom, _, _, _ := out.At(column, AvatarSize-10).RGBA()
	if top>>8 > 80 || bottom>>8 < 175 {
		t.Errorf("top = %d, bottom = %d; want a dark top and light bottom (orientation 6 not applied)", top>>8, bottom>>8)
	}
}

func TestExifOrientationDefaultsToUpright(t *testing.T) {
	cases := map[string][]byte{
		"not a jpeg":        pngOf(t, 4, 4, color.Black),
		"jpeg without exif": jpegOf(t, 8, 8),
		"truncated":         {0xFF, 0xD8, 0xFF},
		"empty":             nil,
	}
	for name, data := range cases {
		if got := exifOrientation(data); got != 1 {
			t.Errorf("%s: exifOrientation = %d, want 1", name, got)
		}
	}
}

func TestAvatarURLVersionsOnUpdatedAt(t *testing.T) {
	const path = "/api/v1/me/avatar"
	at := time.Unix(1700000000, 0)
	cases := []struct {
		name      string
		hasAvatar bool
		updatedAt *time.Time
		want      string
	}{
		{"no avatar", false, &at, ""},
		{"versioned", true, &at, path + "?v=1700000000"},
		{"no timestamp", true, nil, path + "?v=0"},
	}
	for _, tc := range cases {
		if got := AvatarURL(path, tc.hasAvatar, tc.updatedAt); got != tc.want {
			t.Errorf("%s: AvatarURL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWriteAvatarLocksDownTheResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte{1, 2, 3}
	WriteAvatar(rec, httptest.NewRequest("GET", "/me/avatar", nil), Avatar{Bytes: body, ContentType: AvatarContentType})

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Error("body does not match the avatar bytes")
	}
	want := map[string]string{
		"Content-Type":           AvatarContentType,
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "private, max-age=300",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("Content-Security-Policy is not set")
	}
}
