// Package images converts chip photos into a format every browser can
// render inline. Ported from go-passport-issuer's backend/images converter,
// which solves the same problem for the same source data (ICAO 9303 DG2/DG6
// chip portraits).
package images

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"log/slog"

	"github.com/gmrtd/gmrtd/utils"
)

// ToDisplayablePNG converts a JPEG2000 chip portrait (common in DG2, per ICAO
// 9303) to PNG, since browsers do not render JPEG2000. Anything else, a failed
// decode included, comes back unchanged: this is for display, not a
// validation.
func ToDisplayablePNG(imageBase64, mimeType string) (string, string, error) {
	if imageBase64 == "" {
		return imageBase64, mimeType, nil
	}

	raw, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", "", fmt.Errorf("decode base64 photo: %w", err)
	}

	format, _ := utils.DetectImageFormat(raw)
	if format == utils.ImageFormatJPEG {
		// The bytes decide, not the type the app claimed: a JPEG labelled
		// image/jp2 would otherwise be dropped as not displayable.
		return imageBase64, string(utils.ImageFormatJPEG), nil
	}
	if format != utils.ImageFormatJPEG2000 {
		return imageBase64, mimeType, nil
	}

	var converted bytes.Buffer
	var encodeErr error
	err = Decode(raw, func(img image.Image, _ string) error {
		encodeErr = png.Encode(&converted, img)
		return encodeErr
	})
	if encodeErr != nil {
		return "", "", fmt.Errorf("encode photo as PNG: %w", encodeErr)
	}
	if err != nil {
		slog.Warn("images: JPEG2000 photo not converted for display; returning original bytes", slog.Any("error", err))
		return imageBase64, mimeType, nil
	}

	return base64.StdEncoding.EncodeToString(converted.Bytes()), "image/png", nil
}
