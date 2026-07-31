package user

// The blank image imports register the decoders for the formats an avatar upload
// may use. SVG is deliberately absent: an avatar is a portrait photo, and an
// uploaded SVG is an XML document that would have to be served back to other
// members.
import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// MaxAvatarUploadBytes caps the uploaded file. A photo straight off a phone is
	// a few MiB; the stored avatar is re-encoded to AvatarSize square regardless,
	// so this only bounds what the server reads and decodes.
	MaxAvatarUploadBytes = 8 << 20
	// maxSourcePixels rejects a decompression bomb — a small file that decodes to
	// an enormous bitmap. 24 megapixels is above any phone camera we expect.
	maxSourcePixels = 24_000_000
	// AvatarSize is the stored edge length in pixels. Avatars render at 28-80px,
	// so 256 stays sharp on a high-DPI screen without keeping a large image.
	AvatarSize = 256
	// avatarQuality is the JPEG quality of the stored avatar: visually clean at a
	// few tens of KiB for a 256px photo.
	avatarQuality = 88
	// AvatarContentType is the type every stored avatar is re-encoded to.
	AvatarContentType = "image/jpeg"
	// avatarCacheSeconds is how long a browser may reuse a fetched avatar. The URL
	// carries an updated-at version, so a replaced photo is a different URL.
	avatarCacheSeconds = 300
)

var (
	// ErrNoAvatar is returned when the user has no avatar stored.
	ErrNoAvatar = errors.New("user: no avatar")
	// ErrAvatarUnsupported means the bytes are not a decodable image in a format
	// we accept.
	ErrAvatarUnsupported = errors.New("user: unsupported avatar image")
	// ErrAvatarTooLarge means the image decodes to more pixels than we process.
	ErrAvatarTooLarge = errors.New("user: avatar image has too many pixels")
)

// Avatar is a user's stored portrait photo. Bytes always hold the normalised
// image produced by NormalizeAvatar, never the raw upload.
type Avatar struct {
	Bytes       []byte
	ContentType string
}

// NormalizeAvatar turns an uploaded photo into the one shape we store: a square
// AvatarSize×AvatarSize JPEG. Re-encoding is what makes the avatar safe to keep
// and to serve — it drops every metadata block the camera wrote (EXIF, so also
// the GPS coordinates of where the photo was taken), bounds the stored size, and
// leaves one content type to serve. The visible orientation is preserved: the
// EXIF orientation tag is read before it is discarded and applied to the pixels.
func NormalizeAvatar(data []byte) (Avatar, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Avatar{}, ErrAvatarUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Avatar{}, ErrAvatarUnsupported
	}
	if cfg.Width*cfg.Height > maxSourcePixels {
		return Avatar{}, ErrAvatarTooLarge
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Avatar{}, ErrAvatarUnsupported
	}

	// Crop and scale before re-orienting: a centred square and a uniform scale
	// both commute with the eight EXIF orientations, so the pixel-by-pixel
	// remapping runs over the AvatarSize thumbnail instead of the full photo.
	square := image.NewRGBA(image.Rect(0, 0, AvatarSize, AvatarSize))
	draw.Draw(square, square.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(square, square.Bounds(), src, centerSquare(src.Bounds()), draw.Over, nil)
	oriented := applyOrientation(square, exifOrientation(data))

	var out bytes.Buffer
	if err := jpeg.Encode(&out, oriented, &jpeg.Options{Quality: avatarQuality}); err != nil {
		return Avatar{}, fmt.Errorf("user: encode avatar: %w", err)
	}
	return Avatar{Bytes: out.Bytes(), ContentType: AvatarContentType}, nil
}

// centerSquare is the largest centred square inside r, so a portrait or
// landscape photo is cropped rather than squashed into the circular frame.
func centerSquare(r image.Rectangle) image.Rectangle {
	edge := min(r.Dx(), r.Dy())
	offX := (r.Dx() - edge) / 2
	offY := (r.Dy() - edge) / 2
	return image.Rect(r.Min.X+offX, r.Min.Y+offY, r.Min.X+offX+edge, r.Min.Y+offY+edge)
}

// applyOrientation rewrites the pixels so the image reads upright without the
// EXIF tag that said so. The eight cases are the EXIF Orientation values: 1 is
// already upright, 2/4 mirror, 3 rotates 180°, and 5-8 transpose (so width and
// height swap). Anything outside that range is treated as upright.
func applyOrientation(src image.Image, orientation int) image.Image {
	const (
		upright         = 1
		lastOrientation = 8
		firstTranspose  = 5
	)
	if orientation <= upright || orientation > lastOrientation {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	outW, outH := w, h
	if orientation >= firstTranspose {
		outW, outH = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := range h {
		for x := range w {
			var nx, ny int
			switch orientation {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation reads the EXIF Orientation tag out of a JPEG's APP1 segment,
// returning 1 (upright) when the image is not a JPEG or carries no readable tag.
// Only this one tag is parsed — everything else in the EXIF block is dropped by
// the re-encode, which is the point.
func exifOrientation(data []byte) int {
	const upright = 1
	app1, ok := jpegAPP1(data)
	if !ok {
		return upright
	}
	orientation, ok := tiffOrientation(app1)
	if !ok {
		return upright
	}
	return orientation
}

// jpegAPP1 walks the JPEG marker segments and returns the payload of the first
// APP1 segment that starts with the "Exif\x00\x00" identifier.
func jpegAPP1(data []byte) ([]byte, bool) {
	const (
		markerPrefix   = 0xFF
		markerAPP1     = 0xE1
		markerSOS      = 0xDA
		markerSOI      = 0xD8
		segmentLenSize = 2
	)
	exifHeader := []byte("Exif\x00\x00")

	if len(data) < segmentLenSize || data[0] != markerPrefix || data[1] != markerSOI {
		return nil, false
	}
	i := segmentLenSize
	for i+3 < len(data) {
		if data[i] != markerPrefix {
			return nil, false
		}
		marker := data[i+1]
		if marker == markerSOS {
			return nil, false
		}
		size := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if size < segmentLenSize || i+segmentLenSize+size > len(data) {
			return nil, false
		}
		payload := data[i+4 : i+segmentLenSize+size]
		if marker == markerAPP1 && bytes.HasPrefix(payload, exifHeader) {
			return payload[len(exifHeader):], true
		}
		i += segmentLenSize + size
	}
	return nil, false
}

// tiffOrientation reads tag 0x0112 (Orientation) from the first IFD of a TIFF
// header, which is how EXIF stores it. Returns ok=false on anything malformed —
// a missing orientation is normal, not an error worth failing the upload over.
func tiffOrientation(tiff []byte) (int, bool) {
	const (
		headerSize      = 8
		entrySize       = 12
		countSize       = 2
		orientationTag  = 0x0112
		shortType       = 3
		littleEndianTag = 0x4949
		bigEndianTag    = 0x4D4D
	)
	if len(tiff) < headerSize {
		return 0, false
	}
	var order binary.ByteOrder
	switch binary.BigEndian.Uint16(tiff[0:2]) {
	case littleEndianTag:
		order = binary.LittleEndian
	case bigEndianTag:
		order = binary.BigEndian
	default:
		return 0, false
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < headerSize || ifdOffset+countSize > len(tiff) {
		return 0, false
	}
	entries := int(order.Uint16(tiff[ifdOffset : ifdOffset+countSize]))
	base := ifdOffset + countSize
	for n := range entries {
		at := base + n*entrySize
		if at+entrySize > len(tiff) {
			return 0, false
		}
		if order.Uint16(tiff[at:at+2]) != orientationTag {
			continue
		}
		if order.Uint16(tiff[at+2:at+4]) != shortType {
			return 0, false
		}
		// A SHORT value is stored in the first two bytes of the 4-byte value field.
		return int(order.Uint16(tiff[at+8 : at+10])), true
	}
	return 0, false
}

// AvatarURL is the API path that serves an avatar, or "" when none is stored.
// The updated-at timestamp is a cache-busting version, so replacing a photo
// changes the URL instead of leaving the old one cached in the browser.
func AvatarURL(path string, hasAvatar bool, updatedAt *time.Time) string {
	if !hasAvatar {
		return ""
	}
	version := "0"
	if updatedAt != nil {
		version = strconv.FormatInt(updatedAt.Unix(), 10)
	}
	return path + "?v=" + version
}

// WriteAvatar streams an avatar with a locked-down response. The bytes are
// always a re-encoded JPEG served same-origin, so nosniff keeps the declared
// type authoritative and the sandbox + null-source CSP leave nothing to run if
// the URL is opened directly. Caching is private: an avatar is personal data and
// must not be held by a shared cache.
func WriteAvatar(w http.ResponseWriter, r *http.Request, avatar Avatar) {
	h := w.Header()
	h.Set("Content-Type", avatar.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(avatarCacheSeconds))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(avatar.Bytes); err != nil {
		// The status and headers are already committed, so an error here can only
		// be logged, not turned into an API error response.
		slog.ErrorContext(r.Context(), "user: write avatar body", slog.String("error", err.Error()))
	}
}
