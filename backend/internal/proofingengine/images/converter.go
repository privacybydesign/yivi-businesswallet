// Package images converts chip photos into a format every browser can
// render inline. Ported from go-passport-issuer's backend/images converter,
// which solves the same problem for the same source data (ICAO 9303 DG2/DG6
// chip portraits).
package images

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"log/slog"

	"github.com/gmrtd/gmrtd/utils"
)

// ToDisplayablePNG converts a chip photo to a format every browser can
// render inline. Passport/ID-card DG2 portraits are frequently encoded as
// JPEG2000 (image/jp2) per ICAO 9303 — no mainstream browser decodes that
// inline, so an <img> using the raw bytes renders broken even though the
// data is a perfectly valid image (e.g. macOS Preview, which does support
// JP2, opens it fine — which is why downloading the same bytes "works" while
// the in-page preview doesn't). Driving-licence DG6 portraits are almost
// always plain JPEG already, which is why they've never shown this problem.
//
// Anything not detected as JPEG2000 — including a failed decode — is
// returned unchanged. This is a display nicety, not a validation step:
// callers must not treat a non-nil error, or an unconverted pass-through, as
// meaning the photo itself is invalid.
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

	img, err := parseJPEG2000(raw)
	if err != nil {
		slog.Warn("images: JPEG2000 photo not converted for display; returning original bytes", slog.Any("error", err))
		return imageBase64, mimeType, nil
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", fmt.Errorf("encode photo as PNG: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), "image/png", nil
}
