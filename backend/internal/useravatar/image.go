package useravatar

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
)

// jpegQuality re-encodes a JPEG portrait. 85 is the usual "no visible loss at
// this size" setting and keeps a 512px avatar well under MaxAvatarBytes.
const jpegQuality = 85

const (
	formatPNG  = "png"
	formatJPEG = "jpeg"
)

// normalize validates an uploaded avatar and re-encodes it. Re-encoding is the
// point, not a side effect: it drops every metadata block the original carried,
// which for a photo straight off a phone means the EXIF GPS coordinates of where
// it was taken. It also means the stored bytes are output of the Go image
// encoders rather than an attacker-chosen file.
//
// Only PNG and JPEG are accepted. They are what the standard library can decode
// and re-encode, the browser normalises anything else to one of them before
// upload, and unlike the org logo an avatar has no reason to accept SVG (which is
// a scriptable document).
func normalize(data []byte) (Avatar, error) {
	// DecodeConfig reads the header only, so an image that declares enormous
	// dimensions is rejected before any pixel buffer is allocated — a few hundred
	// bytes of PNG can otherwise decode into gigabytes.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Avatar{}, badRequest("invalid_input", "the photo must be a PNG or JPEG image")
	}
	if format != formatPNG && format != formatJPEG {
		return Avatar{}, badRequest("invalid_input", "the photo must be a PNG or JPEG image")
	}
	if cfg.Width > MaxAvatarDimension || cfg.Height > MaxAvatarDimension {
		return Avatar{}, badRequest("invalid_input",
			fmt.Sprintf("the photo must be at most %dx%d pixels", MaxAvatarDimension, MaxAvatarDimension))
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Avatar{}, badRequest("invalid_input", "the photo could not be read as an image")
	}

	var out bytes.Buffer
	contentType := "image/jpeg"
	if format == formatPNG {
		contentType = "image/png"
		if err := png.Encode(&out, img); err != nil {
			return Avatar{}, fmt.Errorf("useravatar: re-encode png: %w", err)
		}
	} else if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return Avatar{}, fmt.Errorf("useravatar: re-encode jpeg: %w", err)
	}

	// Re-encoding can grow a file the uploader had already optimised, so the cap
	// is enforced on what is actually stored, not only on what arrived.
	if out.Len() > MaxAvatarBytes {
		return Avatar{}, apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the photo is too large")
	}
	return Avatar{Bytes: out.Bytes(), ContentType: contentType}, nil
}
