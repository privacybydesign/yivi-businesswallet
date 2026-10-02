package images

import (
	"bytes"
	"fmt"
	"image"

	jpeg2000 "github.com/mrjoshuak/go-jpeg2000"
)

// parseJPEG2000 decodes a JP2 file or raw J2K codestream in pure Go, so the
// build needs no cgo (IPS used ImageMagick for this).
func parseJPEG2000(raw []byte) (image.Image, error) {
	img, err := jpeg2000.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("images: decode JPEG2000: %w", err)
	}
	return img, nil
}
