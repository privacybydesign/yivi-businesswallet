package signing

import (
	"errors"
	"testing"
)

func TestReadGeometryReportsEveryPagesBox(t *testing.T) {
	geometry, err := readGeometry(buildTestPDF(t, 3, false))
	if err != nil {
		t.Fatalf("readGeometry: %v", err)
	}
	if len(geometry.pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(geometry.pages))
	}
	for i, box := range geometry.pages {
		if box.maxX != testPageWidth || box.maxY != testPageHeight {
			t.Fatalf("page %d box = %+v, want %dx%d", i+1, box, testPageWidth, testPageHeight)
		}
	}
}

// readGeometry is the create-request upload check as well as the source of the
// geometry placements are validated against, so a file that is not a PDF at all has
// to come back as ErrInvalidPDF rather than as a panic from digitorus/pdf.
func TestReadGeometryRejectsWhatIsNotAPDF(t *testing.T) {
	for _, input := range [][]byte{nil, []byte("not a pdf"), []byte("%PDF-1.4\ntrailer\n")} {
		if _, err := readGeometry(input); !errors.Is(err, ErrInvalidPDF) {
			t.Fatalf("readGeometry(%q) = %v, want ErrInvalidPDF", input, err)
		}
	}
}

func TestValidatePlacementsAcceptsASignatureAndAParaphPerPage(t *testing.T) {
	in := []Placement{
		{Kind: PlacementSignature, Page: 2, X: 60, Y: 80, Width: 180, Height: 60},
		{Kind: PlacementParaph, Page: 1, X: 500, Y: 40, Width: 40, Height: 24},
		{Kind: PlacementParaph, Page: 2, X: 500, Y: 40, Width: 40, Height: 24},
	}
	got, err := validatePlacements(in, a4Geometry(2))
	if err != nil {
		t.Fatalf("validatePlacements: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d placements, want %d", len(got), len(in))
	}
	if block := signaturePlacement(got); block == nil || block.Page != 2 {
		t.Fatalf("signaturePlacement = %+v, want the page-2 block", block)
	}
	if paraphs := paraphPlacements(got); len(paraphs) != 2 {
		t.Fatalf("got %d paraphs, want 2", len(paraphs))
	}
}

// No placements is a signer whose signature stays invisible — how every signature
// was applied before placement existed, so it must not become an error.
func TestValidatePlacementsAcceptsNone(t *testing.T) {
	got, err := validatePlacements(nil, a4Geometry(1))
	if err != nil || got != nil {
		t.Fatalf("validatePlacements(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestValidatePlacementsRejects(t *testing.T) {
	tests := []struct {
		name string
		in   []Placement
	}{
		{"an unknown kind", []Placement{{Kind: "initials", Page: 1, X: 10, Y: 10, Width: 40, Height: 20}}},
		{"page zero", []Placement{{Kind: PlacementParaph, Page: 0, X: 10, Y: 10, Width: 40, Height: 20}}},
		{"a page the document does not have", []Placement{
			{Kind: PlacementParaph, Page: 3, X: 10, Y: 10, Width: 40, Height: 20},
		}},
		{"a rectangle off the right edge", []Placement{
			{Kind: PlacementSignature, Page: 1, X: testPageWidth - 10, Y: 10, Width: 40, Height: 20},
		}},
		{"a rectangle below the page", []Placement{
			{Kind: PlacementSignature, Page: 1, X: 10, Y: -30, Width: 40, Height: 20},
		}},
		{"a rectangle too small to aim at", []Placement{
			{Kind: PlacementSignature, Page: 1, X: 10, Y: 10, Width: 40, Height: minPlacementSize - 1},
		}},
		{"a second signature block", []Placement{
			{Kind: PlacementSignature, Page: 1, X: 10, Y: 10, Width: 40, Height: 20},
			{Kind: PlacementSignature, Page: 2, X: 10, Y: 10, Width: 40, Height: 20},
		}},
		{"two paraphs on one page", []Placement{
			{Kind: PlacementParaph, Page: 1, X: 10, Y: 10, Width: 40, Height: 20},
			{Kind: PlacementParaph, Page: 1, X: 80, Y: 10, Width: 40, Height: 20},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validatePlacements(tc.in, a4Geometry(2)); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validatePlacements = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// A page's box is not always anchored at the origin: one whose crop box starts
// below it makes every rectangle on that page negative. The rule is containment in
// the box, not a sign, and nothing between here and the database may add one.
func TestValidatePlacementsAcceptsANegativeOriginPage(t *testing.T) {
	geometry := documentGeometry{pages: []pageBox{{minX: -20, minY: -842, maxX: 575, maxY: 0}}}
	in := []Placement{{Kind: PlacementSignature, Page: 1, X: -10, Y: -800, Width: 180, Height: 56}}
	got, err := validatePlacements(in, geometry)
	if err != nil {
		t.Fatalf("validatePlacements: %v", err)
	}
	if len(got) != 1 || got[0].X != -10 {
		t.Fatalf("got %+v, want the rectangle as placed", got)
	}
}

// readGeometry is the upload check, so what it refuses is what cannot be uploaded at
// all. A page too small to hold a mark is not that: whether a mark fits is a question
// about the mark, and a document is not unreadable because one of its pages is tiny.
func TestReadGeometryAcceptsAPageSmallerThanAMark(t *testing.T) {
	doc := buildTestPDFWith(t, testPDFOptions{
		pages:       1,
		pageEntries: func(int) string { return "/MediaBox [0 0 6 6]" },
	})
	geometry, err := readGeometry(doc)
	if err != nil {
		t.Fatalf("readGeometry: %v", err)
	}
	if len(geometry.pages) != 1 || geometry.pages[0].maxX != 6 {
		t.Fatalf("got %+v, want the 6x6 page measured as it is", geometry.pages)
	}
	// It is the placement that is refused, not the document.
	_, err = validatePlacements([]Placement{
		{Kind: PlacementSignature, Page: 1, X: 0, Y: 0, Width: 6, Height: 6},
	}, geometry)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("validatePlacements = %v, want ErrInvalidRequest", err)
	}
}

// A page with no area was never a page anyone rendered a rectangle against.
func TestReadGeometryRejectsAPageWithNoArea(t *testing.T) {
	doc := buildTestPDFWith(t, testPDFOptions{
		pages:       1,
		pageEntries: func(int) string { return "/MediaBox [0 0 0 842]" },
	})
	if _, err := readGeometry(doc); !errors.Is(err, ErrInvalidPDF) {
		t.Fatalf("readGeometry = %v, want ErrInvalidPDF", err)
	}
}

func TestParaphTextInitialisesALongName(t *testing.T) {
	tests := map[string]string{
		"":                    "?",
		"Jo":                  "Jo",
		"A. Signer":           "AS",
		"Alice Anna van Dijk": "AAVD",
	}
	for name, want := range tests {
		if got := paraphText(name); got != want {
			t.Fatalf("paraphText(%q) = %q, want %q", name, got, want)
		}
	}
}
