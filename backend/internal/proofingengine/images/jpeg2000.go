package images

import (
	"bytes"
	"errors"
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

// maxJPEG2000Components bounds a JPEG2000's colour components: the decoder
// holds a plane per component, so a header naming thousands of them makes a
// small image decode to gigabytes. A portrait is grey or colour, with alpha
// at most.
const maxJPEG2000Components = 4

// MaxImagePixels bounds any other image's raster, checked on its header
// before the full decode.
const MaxImagePixels = 40_000_000

// maxConcurrentDecodes is how many images decode at once in the process: each
// decode holds a full raster, so a burst of uploads queues instead of
// allocating them all at the same time.
const maxConcurrentDecodes = 4

// The names go-jpeg2000 registers its formats under with the image package.
const (
	jp2Format = "jp2"
	j2kFormat = "j2k"
)

// decodeSlots is the process-wide decode semaphore of maxConcurrentDecodes.
var decodeSlots = make(chan struct{}, maxConcurrentDecodes)

// ErrTooLarge is an image whose header promises more than Decode allows.
var ErrTooLarge = errors.New("images: image too large to decode")

// Decode decodes an untrusted image after checking its header: at most
// MaxImagePixels (MaxJPEG2000Pixels and maxJPEG2000Components for a
// JPEG2000), and at most maxConcurrentDecodes at once. It returns the image
// and its format name, as image.Decode does. Every decode of an uploaded
// image goes through it.
func Decode(raw []byte) (image.Image, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("images: unsupported or corrupt image: %w", err)
	}
	limit := int64(MaxImagePixels)
	if format == jp2Format || format == j2kFormat {
		limit = MaxJPEG2000Pixels
		m, err := jpeg2000.DecodeMetadata(bytes.NewReader(raw))
		if err != nil {
			return nil, "", fmt.Errorf("images: read JPEG2000 header: %w", err)
		}
		if m.NumComponents <= 0 || m.NumComponents > maxJPEG2000Components {
			return nil, "", fmt.Errorf("%w: JPEG2000 with %d components", ErrTooLarge, m.NumComponents)
		}
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > limit {
		return nil, "", fmt.Errorf("%w: %dx%d is over %d pixels", ErrTooLarge, cfg.Width, cfg.Height, limit)
	}

	decodeSlots <- struct{}{}
	defer func() { <-decodeSlots }()
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("images: decode %s: %w", format, err)
	}
	return img, format, nil
}
