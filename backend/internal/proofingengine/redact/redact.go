// Package redact covers parts of evidence images under the redaction policy,
// before a result or stored evidence is written, so a kept image is redacted
// from the moment it is stored.
//
// Both are covered solid, never pixelated or blurred. A face (Image) in a
// mosaic or blur can still be matched against candidate photos by a
// recognition model, and nine digits in a known font (Region, the BSN) can be
// read back by rendering candidates through the same grid.
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

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
)

// Rect is a region of an image in [0,1] coordinates from the top left, so it
// holds at any resolution. Values outside [0,1] are clamped.
type Rect struct {
	X, Y, W, H float64
}

// redactionColor covers a Region.
var redactionColor = color.RGBA{A: 0xff}

// faceColor covers a face: a neutral grey, so the evidence reads as a photo
// withheld rather than a broken image.
var faceColor = color.RGBA{R: 0x9e, G: 0x9e, B: 0x9e, A: 0xff}

// MIME types the redacted images are encoded as.
const (
	mimePNG  = "image/png"
	mimeJPEG = "image/jpeg"
)

// Image replaces a whole image, a portrait or selfie (the face crop itself),
// with a PNG of the same size in faceColor. Only the header is read, for the
// size: the image is never decoded.
func Image(imageBase64, mimeType string) (string, string, error) {
	if imageBase64 == "" {
		return imageBase64, mimeType, nil
	}
	raw, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", "", fmt.Errorf("redact: decode base64 image: %w", err)
	}
	cfg, _, err := images.Config(raw)
	if err != nil {
		return "", "", fmt.Errorf("redact: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, flat{bounds: image.Rect(0, 0, cfg.Width, cfg.Height), color: faceColor}); err != nil {
		return "", "", fmt.Errorf("redact: encode covered image: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), mimePNG, nil
}

// flat is an image of one colour, encoded without allocating a raster.
type flat struct {
	bounds image.Rectangle
	color  color.RGBA
}

func (f flat) ColorModel() color.Model { return color.RGBAModel }
func (f flat) Bounds() image.Rectangle { return f.bounds }
func (f flat) At(int, int) color.Color { return f.color }

// Region fills r within the image with a solid block and leaves the rest, as
// for the printed BSN of a document photo.
func Region(imageBase64, mimeType string, r Rect) (string, string, error) {
	return apply(imageBase64, mimeType, r, func(img *image.RGBA, bounds image.Rectangle) {
		fill(img, bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, redactionColor)
	})
}

// apply decodes the image, runs redact over r's pixels and re-encodes it.
func apply(imageBase64, mimeType string, r Rect, redact func(*image.RGBA, image.Rectangle)) (string, string, error) {
	if imageBase64 == "" {
		return imageBase64, mimeType, nil
	}
	raw, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", "", fmt.Errorf("redact: decode base64 image: %w", err)
	}
	var out, outMime string
	err = images.Decode(raw, func(img image.Image, format string) error {
		rgba := toRGBA(img)
		redact(rgba, rectToBounds(r, rgba.Bounds().Dx(), rgba.Bounds().Dy()))
		var err error
		out, outMime, err = encode(rgba, format)
		return err
	})
	if err != nil {
		return "", "", fmt.Errorf("redact: %w", err)
	}
	return out, outMime, nil
}

// toRGBA copies img into a freshly allocated *image.RGBA, so a redaction can
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

func fill(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// encode re-encodes rgba: JPEG stays JPEG, anything else becomes PNG (Go ships
// no lossy encoder for the others). It returns base64 and the mime type.
func encode(rgba *image.RGBA, format string) (string, string, error) {
	var buf bytes.Buffer
	mimeType := mimePNG
	var err error
	if strings.EqualFold(format, "jpeg") {
		mimeType = mimeJPEG
		err = jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buf, rgba)
	}
	if err != nil {
		return "", "", fmt.Errorf("encode redacted image: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), mimeType, nil
}
