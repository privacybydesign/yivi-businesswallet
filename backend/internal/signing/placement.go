package signing

import (
	"bytes"

	"github.com/digitorus/pdf"
)

// Placement kinds. A signature placement is the signer's signature block: it
// becomes the visible appearance of their own PAdES signature, so a signer can
// have at most one (a signature dictionary carries exactly one appearance). A
// paraph placement is their initials on one page; "on every page" is expanded by
// the requester into one placement per page, so every placement is one rectangle.
const (
	PlacementSignature = "signature"
	PlacementParaph    = "paraph"
)

// minPlacementSize is the smallest side, in PDF points, a placement may have (a
// bit under 3 mm). pdfsign refuses an appearance rectangle under 1 point; this
// leaves a floor a person could actually have aimed at, so a rectangle that came
// out of a rounding accident is refused at create time instead of being signed.
const minPlacementSize = 8

// Placement is one visible mark of one signer: a rectangle on one page, in PDF
// user-space points with the origin at the page's bottom-left. That is the space
// pdfsign's appearance rectangle is in, so the conversion from viewer coordinates
// happens once — in the placement UI, which is the only place that knows the zoom,
// the page rotation and the crop box the rectangle was drawn at.
type Placement struct {
	Kind   string  `json:"kind"`
	Page   int     `json:"page"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// rect returns the placement as pdfsign's [llx, lly, urx, ury] appearance rectangle.
func (p Placement) rect() [4]float64 {
	return [4]float64{p.X, p.Y, p.X + p.Width, p.Y + p.Height}
}

// pageBox is one page's visible box in PDF user-space points: its CropBox, or its
// MediaBox when it has none. That is the box a viewer shows and therefore the box
// the placement UI measured its rectangles against.
type pageBox struct {
	minX, minY, maxX, maxY float64
}

func (b pageBox) contains(p Placement) bool {
	return p.X >= b.minX && p.Y >= b.minY && p.X+p.Width <= b.maxX && p.Y+p.Height <= b.maxY
}

// documentGeometry is what validating a placement needs from the document: how
// many pages it has and how big each one is.
type documentGeometry struct {
	pages []pageBox
}

// readGeometry parses input as a PDF and reports its page geometry. It doubles as
// the upload check a create-request call makes before storing the document, so a
// file the signing pass could not open — or one whose pages cannot be measured —
// is ErrInvalidPDF here rather than a failure at signing time.
//
// digitorus/pdf reports a malformed xref, trailer or object graph by panicking
// rather than erroring, so every read of it is wrapped: a bad upload is a property
// of the file, not a server fault.
func readGeometry(input []byte) (geometry documentGeometry, err error) {
	defer func() {
		if recover() != nil {
			geometry, err = documentGeometry{}, ErrInvalidPDF
		}
	}()
	rdr, err := pdf.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return documentGeometry{}, ErrInvalidPDF
	}
	count := rdr.NumPage()
	if count < 1 {
		return documentGeometry{}, ErrInvalidPDF
	}
	pages := make([]pageBox, 0, count)
	for i := 1; i <= count; i++ {
		box, ok := boxOf(rdr.Page(i))
		if !ok {
			return documentGeometry{}, ErrInvalidPDF
		}
		pages = append(pages, box)
	}
	return documentGeometry{pages: pages}, nil
}

// boxOf reads one page's visible box, preferring the CropBox over the MediaBox. A
// box with no usable area cannot hold a placement.
func boxOf(page pdf.Page) (pageBox, bool) {
	if page.V.IsNull() {
		return pageBox{}, false
	}
	box, ok := boxValue(inherited(page.V, "CropBox"))
	if !ok {
		box, ok = boxValue(inherited(page.V, "MediaBox"))
	}
	return box, ok
}

// inherited resolves a page attribute that may be set on an ancestor node of the
// page tree instead of the page itself (PDF 32000-1 §7.7.3.4). digitorus/pdf has
// this walk, but its Page.MediaBox/CropBox accessors are commented out.
func inherited(page pdf.Value, key string) pdf.Value {
	for v := page; !v.IsNull(); v = v.Key("Parent") {
		if found := v.Key(key); !found.IsNull() {
			return found
		}
	}
	return pdf.Value{}
}

// boxValue reads a [llx lly urx ury] rectangle, normalising the corners: the
// order of the two corners in a PDF rectangle is not guaranteed. A box with no area
// is refused, because it cannot be the box a page was rendered at — but a small one
// is not: whether a page is big enough for a mark is a question about the mark, and
// validatePlacements answers it. Rejecting the page here would reject the whole
// upload over a page nobody is placing anything on.
func boxValue(v pdf.Value) (pageBox, bool) {
	if v.Kind() != pdf.Array || v.Len() != 4 {
		return pageBox{}, false
	}
	c := [4]float64{}
	for i := range c {
		e := v.Index(i)
		if e.Kind() != pdf.Integer && e.Kind() != pdf.Real {
			return pageBox{}, false
		}
		c[i] = e.Float64()
	}
	box := pageBox{
		minX: min(c[0], c[2]), minY: min(c[1], c[3]),
		maxX: max(c[0], c[2]), maxY: max(c[1], c[3]),
	}
	if box.maxX <= box.minX || box.maxY <= box.minY {
		return pageBox{}, false
	}
	return box, true
}

// validatePlacements checks one signer's placements against the document and
// returns them normalised (nil when the signer placed nothing, which keeps their
// signature invisible — the behaviour before placements existed).
//
// A placement is refused rather than clamped: the rectangle is where a person
// pointed at a rendering of this document, so a value outside the page means the
// two disagree about the document and silently moving the signature somewhere else
// on the page is the one outcome nobody asked for.
func validatePlacements(in []Placement, geometry documentGeometry) ([]Placement, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]Placement, 0, len(in))
	signatures := 0
	paraphPages := make(map[int]bool, len(in))
	for _, p := range in {
		if p.Page < 1 || p.Page > len(geometry.pages) {
			return nil, ErrInvalidRequest
		}
		if p.Width < minPlacementSize || p.Height < minPlacementSize {
			return nil, ErrInvalidRequest
		}
		if !geometry.pages[p.Page-1].contains(p) {
			return nil, ErrInvalidRequest
		}
		switch p.Kind {
		case PlacementSignature:
			signatures++
			if signatures > 1 {
				return nil, ErrInvalidRequest
			}
		case PlacementParaph:
			if paraphPages[p.Page] {
				return nil, ErrInvalidRequest
			}
			paraphPages[p.Page] = true
		default:
			return nil, ErrInvalidRequest
		}
		out = append(out, p)
	}
	return out, nil
}

// signaturePlacement returns the signer's signature block, or nil when they have
// none (their signature then stays invisible, as it was before placements).
func signaturePlacement(placements []Placement) *Placement {
	for i := range placements {
		if placements[i].Kind == PlacementSignature {
			return &placements[i]
		}
	}
	return nil
}

// paraphPlacements returns the signer's paraph rectangles, in page order as the
// store loaded them.
func paraphPlacements(placements []Placement) []Placement {
	out := make([]Placement, 0, len(placements))
	for _, p := range placements {
		if p.Kind == PlacementParaph {
			out = append(out, p)
		}
	}
	return out
}
