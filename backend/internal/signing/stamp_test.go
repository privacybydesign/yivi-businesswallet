package signing

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// testSignedAt is a fixed signing time for the stamp tests that do not care what the
// date reads, only that a mark was drawn or a page survived.
var testSignedAt = time.Date(2026, 8, 11, 14, 32, 0, 0, time.UTC)

// Both a paraph and a signature block are stamps now, and each rewrites the page
// dictionary it lands on. A page dictionary legally carries entries that are not a
// name, a number or a reference: /Metadata and /Thumb are streams, and /LastModified
// is a date string (required whenever /PieceInfo is present, which InDesign and
// Illustrator write routinely). digitorus/pdf hands those back resolved and its
// Value.String() is a debug formatter — a stream comes out as `<<...>>@offset` and a
// string Go-quoted — so a page rewritten through it would stop parsing; writeValue
// emits only forms that are valid PDF.
//
// These are the pages the plain ones buildTestPDF writes do not cover.
var awkwardPageEntries = []struct {
	name    string
	entries func(firstExtraID int) string
	objects []string
}{
	{
		name:    "page metadata stream",
		entries: func(id int) string { return fmt.Sprintf("/Metadata %d 0 R", id) },
		objects: []string{
			"<< /Type /Metadata /Subtype /XML /Length 34 >>\nstream\n" +
				"<?xpacket begin=\"\" id=\"W5M0Mp\"?>\n\nendstream",
		},
	},
	{
		name:    "page thumbnail stream",
		entries: func(id int) string { return fmt.Sprintf("/Thumb %d 0 R", id) },
		objects: []string{
			"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray" +
				" /BitsPerComponent 8 /Length 1 >>\nstream\n\x00\nendstream",
		},
	},
	{
		// What an InDesign page looks like: private application data, and the date
		// string PDF 32000-1 §14.5 requires alongside it.
		name: "page piece info and last modified",
		entries: func(int) string {
			return "/LastModified (D:20240101120000Z) /PieceInfo << /Test" +
				" << /LastModified (D:20240101120000Z) /Private 1 >> >>"
		},
	},
	{
		// A page identifier is a byte string in PDF 2.0, and a producer may write one
		// that is not text at all.
		name:    "page id string",
		entries: func(int) string { return "/ID <DEADBEEF>" },
	},
}

func awkwardPageDocument(t *testing.T, pages int, index int) []byte {
	t.Helper()
	c := awkwardPageEntries[index]
	return buildTestPDFWith(t, testPDFOptions{
		pages:        pages,
		pageEntries:  c.entries,
		extraObjects: c.objects,
	})
}

// TestParaphStampKeepsAnAwkwardPageReadable is the paraph half: the page dictionary
// the stamp rewrites has to come back out as PDF, whatever the producer put in it.
func TestParaphStampKeepsAnAwkwardPageReadable(t *testing.T) {
	for i, c := range awkwardPageEntries {
		t.Run(c.name, func(t *testing.T) {
			doc := awkwardPageDocument(t, 1, i)
			if _, err := readGeometry(doc); err != nil {
				t.Fatalf("the test document itself does not parse: %v", err)
			}

			stamped, err := stampSignerMarks(doc, []Placement{
				{Kind: PlacementParaph, Page: 1, X: 500, Y: 40, Width: 48, Height: 24},
			}, "AB", testSignedAt)
			if err != nil {
				t.Fatalf("stamp paraph: %v", err)
			}
			if _, err := readGeometry(stamped); err != nil {
				t.Fatalf("the stamped document no longer parses: %v", err)
			}
			if !bytes.Contains(stamped, []byte("/Subtype /Stamp")) {
				t.Fatal("the stamped document carries no paraph annotation")
			}
		})
	}
}

// TestSignatureBlockKeepsAnAwkwardPageReadable is the signature half. The signature
// block is our own stamp, so the page it lands on goes through our rewrite — the same
// one the paraph uses — and writeValue re-emits every awkward entry (a metadata
// stream as a reference, a /LastModified date as a hex string) as valid PDF.
func TestSignatureBlockKeepsAnAwkwardPageReadable(t *testing.T) {
	for i, c := range awkwardPageEntries {
		t.Run(c.name, func(t *testing.T) {
			signed := signWithPlacements(t, awkwardPageDocument(t, 1, i), []Placement{
				{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
				{Kind: PlacementParaph, Page: 1, X: 500, Y: 40, Width: 48, Height: 24},
			})
			mustVerify(t, signed, 1)
			geometry, err := readGeometry(signed)
			if err != nil {
				t.Fatalf("the signed document no longer parses: %v", err)
			}
			if len(geometry.pages) != 1 {
				t.Fatalf("got %d pages after signing, want 1", len(geometry.pages))
			}
		})
	}
}

// The signature block is now our own stamp inside an invisible signature, so no page
// is ever handed to pdfsign's fragile rewrite. A page entry pdfsign could not re-emit
// — here an additional-action holding a JavaScript string — is kept verbatim by our
// own writeValue, so the document signs and the entry survives rather than being
// refused or silently dropped.
func TestSignaturePageEntryPdfsignCouldNotEmitSurvives(t *testing.T) {
	doc := buildTestPDFWith(t, testPDFOptions{
		pages: 1,
		pageEntries: func(int) string {
			return "/AA << /O << /S /JavaScript /JS (app.alert\\(1\\)) >> >>"
		},
	})
	if _, err := readGeometry(doc); err != nil {
		t.Fatalf("the test document itself does not parse: %v", err)
	}

	signed := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
	})
	mustVerify(t, signed, 1)
	if _, err := readGeometry(signed); err != nil {
		t.Fatalf("the signed document no longer parses: %v", err)
	}
	// The action is a name plus a JavaScript string; writeValue keeps the string as a
	// hex literal, so the /S /JavaScript key is still there.
	if !bytes.Contains(signed, []byte("/JavaScript")) {
		t.Error("the page's /AA action did not survive signing")
	}
}

// An /Annots array may hold an annotation dictionary directly rather than a
// reference to one. pdfsign's rewrite writes every entry as a reference, so a direct
// one on the signature page has to become an object of its own first — otherwise it
// comes back as a reference to the page.
func TestDirectAnnotationOnTheSignaturePageSurvives(t *testing.T) {
	doc := buildTestPDFWith(t, testPDFOptions{
		pages: 1,
		pageEntries: func(int) string {
			return "/Annots [ << /Type /Annot /Subtype /Square /Rect [10 10 30 30] /F 4 >> ]"
		},
	})
	signed := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
	})
	mustVerify(t, signed, 1)

	rdr, err := openPDF(signed)
	if err != nil {
		t.Fatalf("open signed document: %v", err)
	}
	annots := rdr.Page(1).V.Key("Annots")
	found := false
	for i := 0; i < annots.Len(); i++ {
		if annots.Index(i).Key("Subtype").Name() == "Square" {
			found = true
		}
	}
	if !found {
		t.Fatal("the page's own annotation did not survive the signature")
	}
}

// A producer that reuses an object number writes the reused object at a higher
// generation — legal PDF, and the case pdfsign's own page rewrite could not handle
// (it emits every reference at generation 0). The signature block no longer goes
// through that rewrite: it is our own stamp, and our rewrite keeps each reference and
// the page object at the generation it was read at. So a document with a reused object
// number on its signature page signs and still draws, where it used to be refused.
func TestSignaturePageWithAReusedObjectNumberSigns(t *testing.T) {
	tests := []struct {
		name     string
		gens     map[int]int
		contents func(i, firstExtraID int) string
		objects  []string
	}{
		{
			name: "content stream",
			gens: map[int]int{testPDFContentID(0): 1},
		},
		{
			name:     "content stream in an array",
			gens:     map[int]int{testPDFFirstExtraID(1): 1},
			contents: func(_, id int) string { return fmt.Sprintf("[ %d 1 R ]", id) },
			objects:  []string{"<< /Length 16 >>\nstream\nBT /F1 12 Tf ET\nendstream"},
		},
		{
			name: "page tree node",
			gens: map[int]int{testPDFPagesID: 1},
		},
		{
			name: "the page dictionary itself",
			gens: map[int]int{testPDFPageID(0): 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := buildTestPDFWith(t, testPDFOptions{
				pages:        1,
				objectGens:   tc.gens,
				pageContents: tc.contents,
				extraObjects: tc.objects,
			})
			if _, err := readGeometry(doc); err != nil {
				t.Fatalf("the test document itself does not parse: %v", err)
			}

			signed := signWithPlacements(t, doc, []Placement{
				{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
			})
			mustVerify(t, signed, 1)
			rdr, err := openPDF(signed)
			if err != nil {
				t.Fatalf("open signed document: %v", err)
			}
			if rdr.Page(1).V.Key("Contents").IsNull() {
				t.Error("signing blanked the page")
			}
		})
	}
}

// The /Annots half of the same problem is fixable rather than fatal: an entry at a
// reused object number is copied into an object of its own at generation 0, which is
// a reference pdfsign can write back, so the annotation survives.
func TestReusedAnnotationOnTheSignaturePageSurvives(t *testing.T) {
	annotID := testPDFFirstExtraID(1)
	doc := buildTestPDFWith(t, testPDFOptions{
		pages:       1,
		pageEntries: func(id int) string { return fmt.Sprintf("/Annots [ %d 1 R ]", id) },
		extraObjects: []string{
			"<< /Type /Annot /Subtype /Square /Rect [10 10 30 30] /F 4 >>",
		},
		objectGens: map[int]int{annotID: 1},
	})
	if _, err := readGeometry(doc); err != nil {
		t.Fatalf("the test document itself does not parse: %v", err)
	}

	signed := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
	})
	mustVerify(t, signed, 1)

	rdr, err := openPDF(signed)
	if err != nil {
		t.Fatalf("open signed document: %v", err)
	}
	page := rdr.Page(1).V
	if page.Key("Contents").IsNull() {
		t.Error("the page came back blank")
	}
	annots := page.Key("Annots")
	found := false
	for i := 0; i < annots.Len(); i++ {
		if annots.Index(i).Key("Subtype").Name() == "Square" {
			found = true
		}
	}
	if !found {
		t.Errorf("the page's own annotation did not survive the signature (%d entries)", annots.Len())
	}
}

// The paraph is drawn from the signer's name, and a Dutch or EU name carries
// diacritics. The font declares WinAnsiEncoding, so the bytes in the content stream
// have to be WinAnsi and not the UTF-8 they arrive as — Ü is one byte, and reading
// its two UTF-8 bytes in the default encoding draws a macron and a notdef.
func TestParaphTextIsEncodedForTheFontItIsDrawnIn(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Ünal", "\xdcnal"},
		{"José Álvarez", "J\xc1"},
		{"Jörg", "J\xf6rg"},
		{"Ελένη Παπαδοπουλου", "??"}, // outside WinAnsi: a defined substitute
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := winAnsi(paraphText(tc.name))
			if string(got) != tc.want {
				t.Fatalf("winAnsi(paraphText(%q)) = % x, want % x", tc.name, got, tc.want)
			}
			// And the box is measured in the glyphs that are drawn, one per byte.
			appearance := paraphAppearance(
				Placement{Kind: PlacementParaph, Page: 1, Width: 48, Height: 24},
				paraphText(tc.name), 9)
			if !bytes.Contains(appearance, []byte(pdfLiteral(got)+" Tj")) {
				t.Fatalf("the appearance stream does not draw %q:\n%s", tc.want, appearance)
			}
		})
	}
	if !bytes.Contains(paraphFont(), []byte("/Encoding /WinAnsiEncoding")) {
		t.Fatal("the paraph font does not state the encoding its bytes are in")
	}
}

// /Contents is a PDF text string rather than content-stream bytes, so it carries a
// name in any script — including one WinAnsi has no glyphs for.
func TestParaphAnnotationCarriesTheNameAsATextString(t *testing.T) {
	annotation := stampAnnotation(
		Placement{Kind: PlacementParaph, Page: 1, Width: 48, Height: 24}, "JÖ", 9, "3 0 R")
	if !bytes.Contains(annotation, []byte("/Contents <FEFF004A00D6>")) {
		t.Fatalf("annotation does not carry the name as UTF-16BE:\n%s", annotation)
	}
}

// No page is handed to pdfsign's page rewrite any more — both marks are our own
// stamps — so nothing is ever stripped from a page: what the producer wrote stays in
// the document, on the paraph page as much as on the signature page.
func TestParaphOnlyPageKeepsItsEntries(t *testing.T) {
	doc := awkwardPageDocument(t, 2, 2) // /LastModified + /PieceInfo on both pages
	signed := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
		{Kind: PlacementParaph, Page: 2, X: 500, Y: 40, Width: 48, Height: 24},
	})
	mustVerify(t, signed, 1)

	rdr, err := openPDF(signed)
	if err != nil {
		t.Fatalf("open signed document: %v", err)
	}
	if got := rdr.Page(2).V.Key("PieceInfo"); got.IsNull() {
		t.Fatal("the paraph page lost /PieceInfo, which nothing needed to touch")
	}
	if got := rdr.Page(2).V.Key("LastModified").RawString(); got != "D:20240101120000Z" {
		t.Fatalf("the paraph page's /LastModified came back as %q", got)
	}
}
