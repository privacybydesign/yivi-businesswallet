package signing

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Both the paraph stamp and the visible signature rewrite the page dictionary they
// land on, and a page dictionary legally carries entries that are not a name, a
// number or a reference: /Metadata and /Thumb are streams, and /LastModified is a
// date string (required whenever /PieceInfo is present, which InDesign and
// Illustrator write routinely). digitorus/pdf hands those back resolved and its
// Value.String() is a debug formatter — a stream comes out as `<<...>>@offset` and a
// string Go-quoted — so a page rewritten through it stops parsing.
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
			}, "AB")
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

// TestVisibleSignatureKeepsAnAwkwardPageReadable is the signature half. The rewrite
// is pdfsign's there, not ours, and it has the same bug: it writes every entry it
// does not special-case through Value.String(). The page is therefore handed to it
// already stripped of the entries it cannot re-emit, so what it writes back parses.
func TestVisibleSignatureKeepsAnAwkwardPageReadable(t *testing.T) {
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

// Only page metadata may be dropped to get a page past pdfsign. An entry that
// changes what the page draws or does is refused instead — before any signature has
// been produced, since the document is prepared before the digest is published.
func TestSignaturePageWithAnUndroppableEntryIsRefused(t *testing.T) {
	doc := buildTestPDFWith(t, testPDFOptions{
		pages: 1,
		// An additional-action holding JavaScript: a string, so pdfsign cannot write
		// it back, and not something to silently remove from a document being signed.
		pageEntries: func(int) string {
			return "/AA << /O << /S /JavaScript /JS (app.alert\\(1\\)) >> >>"
		},
	})
	if _, err := readGeometry(doc); err != nil {
		t.Fatalf("the test document itself does not parse: %v", err)
	}

	_, cred := stubCredential(t)
	_, _, err := startPAdES(doc, cred, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
	})
	if !errors.Is(err, ErrInvalidPDF) {
		t.Fatalf("startPAdES = %v, want ErrInvalidPDF", err)
	}
	if !strings.Contains(err.Error(), "/AA") {
		t.Errorf("the error does not say which entry stopped it: %v", err)
	}

	// The same document signs as before when nobody asked for a visible signature.
	mustVerify(t, signWithPlacements(t, doc, nil), 1)
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
	annotation := paraphAnnotation(
		Placement{Kind: PlacementParaph, Page: 1, Width: 48, Height: 24}, "JÖ", 9, "3 0 R")
	if !bytes.Contains(annotation, []byte("/Contents <FEFF004A00D6>")) {
		t.Fatalf("annotation does not carry the name as UTF-16BE:\n%s", annotation)
	}
}

// A page that only carries a paraph is never handed to pdfsign's page rewrite, so
// nothing has to be stripped from it: what the producer wrote stays in the document.
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
