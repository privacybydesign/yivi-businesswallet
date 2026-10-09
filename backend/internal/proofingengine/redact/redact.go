// Package redact covers parts of evidence images under the redaction policy,
// before a result or stored evidence is written, so a kept image is redacted
// from the moment it is stored.
//
// A face (Image) is pixelated into flat blocks, which keeps it from being
// recognised. A region (Region, the BSN) is filled solid: nine digits in a known
// font can be read back from a mosaic by rendering candidates through the same
// grid.
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

var wholeImage = Rect{X: 0, Y: 0, W: 1, H: 1}

// pixelateDivisions sets how coarse the redaction grid is: each block is
// roughly 1/pixelateDivisions of the redacted region's smaller dimension,
// with a floor (minBlockSize) so a small region (e.g. a tight BSN bounding
// box) still gets meaningfully coarsened rather than a near-identity blur.
const (
	pixelateDivisions = 10
	minBlockSize      = 8
)

// redactionColor covers a Region.
var redactionColor = color.RGBA{A: 0xff}

// Image pixelates a whole image: a portrait or selfie is the face crop itself.
func Image(imageBase64, mimeType string) (string, string, error) {
	return apply(imageBase64, mimeType, wholeImage, pixelate)
}

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
	img, format, err := images.Decode(raw)
	if err != nil {
		return "", "", fmt.Errorf("redact: %w", err)
	}

	rgba := toRGBA(img)
	redact(rgba, rectToBounds(r, rgba.Bounds().Dx(), rgba.Bounds().Dy()))

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

// encode re-encodes rgba: JPEG stays JPEG, anything else becomes PNG (Go ships
// no lossy encoder for the others). It returns base64 and the mime type.
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
