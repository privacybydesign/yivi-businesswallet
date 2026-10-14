package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	jpeg2000 "github.com/mrjoshuak/go-jpeg2000"
)

// MaxJPEG2000Pixels bounds the raster a JPEG2000 photo may decode to. Its
// header is checked against it before decoding: JPEG2000 compresses a flat
// image to almost nothing (an 8000x8000 one is 2.8 KB and decodes to about
// 1 GB), and these bytes come from the app. A chip portrait (ICAO 9303 DG2,
// driving-licence DG6) is well under one megapixel, and JPEG2000 decodes
// slowly: a megapixel of noise, the worst case, takes about a second.
const MaxJPEG2000Pixels = 1_000_000

// maxJPEG2000Components bounds a JPEG2000's colour components: the decoder
// holds a plane per component, so a header naming thousands of them makes a
// small image decode to gigabytes. A portrait is grey or colour, with alpha
// at most.
const maxJPEG2000Components = 4

// MaxImagePixels bounds any other image's raster, checked on its header
// before the full decode. The largest is the app's document photo, scaled to
// 1600 px on its long edge (2.56 MP at most); selfies and portraits are smaller.
const MaxImagePixels = 4_000_000

// maxConcurrentDecodes is how many images are worked on at once in the
// process: each holds a full raster (and its copies) from decode through
// encode, so a burst of uploads queues instead of allocating them all at the
// same time.
const maxConcurrentDecodes = 4

// The names go-jpeg2000 registers its formats under with the image package.
const (
	jp2Format = "jp2"
	j2kFormat = "j2k"
)

// decodeWait bounds how long an image waits for a decode slot: a burst of
// uploads queues for a while, but slots held by decodes that never finish
// turn into ErrBusy instead of requests that never return. A var so tests can
// shorten it.
var decodeWait = 10 * time.Second

// decodeTimeout bounds one decode. The slowest image the header check lets
// through, a JPEG2000 at MaxJPEG2000Pixels, decodes in about a second; one
// still running after this is given up on, and keeps its slot until it does
// end. A var so tests can shorten it.
var decodeTimeout = 10 * time.Second

// decodeImage is image.Decode, a var so tests can stand in a decoder that
// hangs or panics.
var decodeImage = image.Decode

// decodeSlots is the process-wide decode semaphore of maxConcurrentDecodes.
var decodeSlots = make(chan struct{}, maxConcurrentDecodes)

var (
	// ErrTooLarge is an image whose header promises more than Decode allows.
	ErrTooLarge = errors.New("images: image too large to decode")
	// ErrBusy is every decode slot staying taken for decodeWait.
	ErrBusy = errors.New("images: no decode slot came free")
	// ErrDecodeTimeout is a decode still running after decodeTimeout.
	ErrDecodeTimeout = errors.New("images: decode took too long")
	// ErrDecoderPanic is a decoder that panicked on the image.
	ErrDecoderPanic = errors.New("images: decoder failed on the image")
)

// Config reads an untrusted image's header and refuses one over Decode's
// limits: at most MaxImagePixels (MaxJPEG2000Pixels and
// maxJPEG2000Components for a JPEG2000). It never decodes the raster.
func Config(raw []byte) (image.Config, string, error) {
	var cfg image.Config
	var format string
	err := guard(func() error {
		var err error
		cfg, format, err = image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("images: unsupported or corrupt image: %w", err)
		}
		limit := int64(MaxImagePixels)
		if format == jp2Format || format == j2kFormat {
			limit = MaxJPEG2000Pixels
			m, err := jpeg2000.DecodeMetadata(bytes.NewReader(raw))
			if err != nil {
				return fmt.Errorf("images: read JPEG2000 header: %w", err)
			}
			if m.NumComponents <= 0 || m.NumComponents > maxJPEG2000Components {
				return fmt.Errorf("%w: JPEG2000 with %d components", ErrTooLarge, m.NumComponents)
			}
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > limit {
			return fmt.Errorf("%w: %dx%d is over %d pixels", ErrTooLarge, cfg.Width, cfg.Height, limit)
		}
		return nil
	})
	return cfg, format, err
}

// decoded is one decode's outcome, handed over by the goroutine that ran it.
type decoded struct {
	img    image.Image
	format string
	err    error
}

// Decode decodes an untrusted image after checking its header (Config). It
// hands the image and its format name, as image.Decode returns them, to use,
// and returns use's error. At most maxConcurrentDecodes decodes and their use
// run at once, so whatever use derives from the raster is bounded with it.
// Every decode of an uploaded image goes through it.
//
// The decoders read images from the subject's phone, so a slot waited for
// longer than decodeWait is ErrBusy, a decode running longer than
// decodeTimeout is ErrDecodeTimeout, and a decoder panic is ErrDecoderPanic.
func Decode(raw []byte, use func(img image.Image, format string) error) error {
	if _, _, err := Config(raw); err != nil {
		return err
	}
	wait := time.NewTimer(decodeWait)
	defer wait.Stop()
	select {
	case decodeSlots <- struct{}{}:
	case <-wait.C:
		return ErrBusy
	}

	// The decode runs on its own goroutine, so a decoder that hangs costs this
	// call decodeTimeout and not forever. Whoever finishes last frees the slot:
	// this call after use, or the goroutine once it ends after this call gave
	// up on it. A stuck decode keeps its slot, so at most
	// maxConcurrentDecodes ever run.
	var mu sync.Mutex
	abandoned := false
	done := make(chan decoded, 1)
	go func() {
		var out decoded
		out.err = guard(func() error {
			var err error
			out.img, out.format, err = decodeImage(bytes.NewReader(raw))
			return err
		})
		mu.Lock()
		defer mu.Unlock()
		if abandoned {
			<-decodeSlots
			return
		}
		done <- out
	}()

	timeout := time.NewTimer(decodeTimeout)
	defer timeout.Stop()
	var out decoded
	select {
	case out = <-done:
	case <-timeout.C:
		mu.Lock()
		select {
		case out = <-done:
			// It finished as the timer fired: use it after all.
			mu.Unlock()
		default:
			abandoned = true
			mu.Unlock()
			return ErrDecodeTimeout
		}
	}
	defer func() { <-decodeSlots }()
	if out.err != nil {
		return fmt.Errorf("images: decode %s: %w", out.format, out.err)
	}
	return use(out.img, out.format)
}

// guard runs fn and turns a panic in it into ErrDecoderPanic: a decoder bug
// on a crafted image fails that image, not the request, and not the process
// (the decode runs on a goroutine no HTTP server recovers).
func guard(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrDecoderPanic, r)
		}
	}()
	return fn()
}
