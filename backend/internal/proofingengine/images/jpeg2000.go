package images

import (
	"bytes"
	"fmt"
	"image"

	jpeg2000 "github.com/mrjoshuak/go-jpeg2000"
)

// MaxJPEG2000Pixels bounds the raster a JPEG2000 photo may decode to. Its
// header is checked against it before decoding: JPEG2000 compresses a flat
// image to almost nothing (an 8000x8000 one is 2.8 KB and decodes to about
// 1 GB), and these bytes come from the app. A chip portrait (ICAO 9303 DG2,
// driving-licence DG6) is well under one megapixel.
const MaxJPEG2000Pixels = 4_000_000

// parseJPEG2000 decodes a JP2 file or raw J2K codestream in pure Go, so the
// build needs no cgo.
func parseJPEG2000(raw []byte) (image.Image, error) {
	m, err := jpeg2000.DecodeMetadata(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("images: read JPEG2000 header: %w", err)
	}
	if m.Width <= 0 || m.Height <= 0 || int64(m.Width)*int64(m.Height) > MaxJPEG2000Pixels {
		return nil, fmt.Errorf("images: JPEG2000 of %dx%d is over %d pixels", m.Width, m.Height, MaxJPEG2000Pixels)
	}
	img, err := jpeg2000.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("images: decode JPEG2000: %w", err)
	}
	return img, nil
}
