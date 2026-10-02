// Package redact blurs regions of stored evidence images per tenant policy
// (requirements.md §3: "Option to blur the photo and/or the BSN"). This is
// the enforcement point for that policy: called from
// internal/api.buildResult before a session's result is ever written to the
// session store, so a retained image is redacted from the moment it's
// stored, not only when it's later re-rendered to a relying party.
//
// Redaction is pixelation (block-averaging), not a Gaussian blur: it's
// simpler to reason about and verify (a fixed grid of flat-colour blocks),
// and, at the block size used here, just as irreversible for the purpose of
// keeping a face or a printed BSN from being read back out of the stored
// image.
package redact

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"
)

// Rect is a region of an image in normalized [0,1] coordinates, top-left
// origin — resolution independent, so a region located against whatever
// size an image was captured at still lands correctly against whatever size
// ends up stored. Values outside [0,1] are clamped.
type Rect struct {
	X, Y, W, H float64
}

// wholeImage is the Rect covering an entire image — see Image.
var wholeImage = Rect{X: 0, Y: 0, W: 1, H: 1}

// pixelateDivisions sets how coarse the redaction grid is: each block is
// roughly 1/pixelateDivisions of the redacted region's smaller dimension,
// with a floor (minBlockSize) so a small region (e.g. a tight BSN bounding
// box) still gets meaningfully coarsened rather than a near-identity blur.
const (
	pixelateDivisions = 10
	minBlockSize      = 8
)

// maxImagePixels mirrors face.MaxImagePixels (not imported: that package
// pulls in the TFLite cgo binding): an image's header is checked against it
// before the full raster is decoded, so a small, highly compressed upload
// can't make this allocate gigabytes.
const maxImagePixels = 40_000_000

// Image blurs an entire image — used when the stored image already IS the
// sensitive content (e.g. a DG2/selfie face photo is already just a face
// crop, not a face within a wider scene, so there's no smaller region to
// locate first).
func Image(imageBase64, mimeType string) (string, string, error) {
	return Region(imageBase64, mimeType, wholeImage)
}

// Region blurs just r within the image, leaving the rest untouched — used
// to redact a known bounding box (e.g. a document image's BSN text, see
// api.documentImageInfo.BSNRegion) without destroying the rest of the
// evidence image.
//
// imageBase64 is plain (non-data-URL) base64, matching the convention
// photoInfo/documentImageInfo already use elsewhere in this codebase (see
// internal/images.ToDisplayablePNG). Returns the redacted image re-encoded
// in its original format (JPEG stays JPEG, everything else becomes PNG) and
// its mime type.
func Region(imageBase64, mimeType string, r Rect) (string, string, error) {
	if imageBase64 == "" {
		return imageBase64, mimeType, nil
	}
	raw, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", "", fmt.Errorf("redact: decode base64 image: %w", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("redact: unsupported or corrupt image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxImagePixels {
		return "", "", fmt.Errorf("redact: image dimensions %dx%d exceed the %d pixel limit", cfg.Width, cfg.Height, maxImagePixels)
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("redact: unsupported or corrupt image: %w", err)
	}

	rgba := toRGBA(img)
	pixelate(rgba, rectToBounds(r, rgba.Bounds().Dx(), rgba.Bounds().Dy()))

	return encode(rgba, format)
}

// toRGBA copies img into a freshly allocated *image.RGBA, so pixelate can
// mutate it in place regardless of img's original concrete type.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	return out
}

// rectToBounds converts a normalized Rect into pixel bounds within a w×h
// image, clamping to the image and guaranteeing a non-empty rectangle.
func rectToBounds(r Rect, w, h int) image.Rectangle {
	clamp01 := func(v float64) float64 {
		switch {
		case v < 0:
			return 0
		case v > 1:
			return 1
		default:
			return v
		}
	}
	x0 := int(clamp01(r.X) * float64(w))
	y0 := int(clamp01(r.Y) * float64(h))
	x1 := int(clamp01(r.X+r.W) * float64(w))
	y1 := int(clamp01(r.Y+r.H) * float64(h))
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	return image.Rect(x0, y0, x1, y1)
}

// pixelate block-averages bounds within img, in place.
func pixelate(img *image.RGBA, bounds image.Rectangle) {
	dx, dy := bounds.Dx(), bounds.Dy()
	blockSize := max(min(dx, dy)/pixelateDivisions, minBlockSize)

	for by := bounds.Min.Y; by < bounds.Max.Y; by += blockSize {
		blockMaxY := min(by+blockSize, bounds.Max.Y)
		for bx := bounds.Min.X; bx < bounds.Max.X; bx += blockSize {
			blockMaxX := min(bx+blockSize, bounds.Max.X)
			avg := averageColor(img, bx, by, blockMaxX, blockMaxY)
			fill(img, bx, by, blockMaxX, blockMaxY, avg)
		}
	}
}

func averageColor(img *image.RGBA, x0, y0, x1, y1 int) color.RGBA {
	var rSum, gSum, bSum, aSum, n uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			rSum += uint64(c.R)
			gSum += uint64(c.G)
			bSum += uint64(c.B)
			aSum += uint64(c.A)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{}
	}
	return color.RGBA{R: uint8(rSum / n), G: uint8(gSum / n), B: uint8(bSum / n), A: uint8(aSum / n)}
}

func fill(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// encode re-encodes rgba as format ("jpeg" stays JPEG at a high quality;
// anything else — png, gif, webp — becomes PNG, since Go's standard library
// only ships a lossless encoder for those), returning base64 and the
// matching mime type.
func encode(rgba *image.RGBA, format string) (string, string, error) {
	var buf bytes.Buffer
	mimeType := "image/png"
	var err error
	if strings.EqualFold(format, "jpeg") {
		mimeType = "image/jpeg"
		err = jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buf, rgba)
	}
	if err != nil {
		return "", "", fmt.Errorf("redact: encode redacted image: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), mimeType, nil
}
