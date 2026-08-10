package signing

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"testing"
)

// The placement tests need documents the repository's single-page sample.pdf cannot
// stand in for: several pages (a paraph on every page), and a cross-reference stream
// rather than a table (the other of the two section kinds an appended revision has
// to match). Both are built here rather than checked in as fixtures, so what makes a
// case what it is stays readable.

const (
	testPageWidth  = 595 // A4 in PDF points, near enough
	testPageHeight = 842
)

// a4Geometry is the geometry of a document of A4 pages, for validating placements
// without building a document to measure.
func a4Geometry(pages int) documentGeometry {
	boxes := make([]pageBox, pages)
	for i := range boxes {
		boxes[i] = pageBox{maxX: testPageWidth, maxY: testPageHeight}
	}
	return documentGeometry{pages: boxes}
}

// testPDFOptions describes the document buildTestPDF writes.
type testPDFOptions struct {
	pages int
	// xrefStream selects a cross-reference stream over a table.
	xrefStream bool
	// extraObjects are written after the pages, numbered from the first free object
	// id. pageEntries is handed that id, so a page dictionary can point at one.
	extraObjects []string
	// pageEntries are appended to every page dictionary, which is how a case gets a
	// page carrying the sort of entry a real producer writes.
	pageEntries func(firstExtraID int) string
}

// buildTestPDF writes a valid PDF of the given number of A4 pages, each carrying a
// line of text. xrefStream selects a cross-reference stream over a table.
func buildTestPDF(t *testing.T, pages int, xrefStream bool) []byte {
	t.Helper()
	return buildTestPDFWith(t, testPDFOptions{pages: pages, xrefStream: xrefStream})
}

// buildTestPDFWith is buildTestPDF with the page dictionaries and the object list
// under the caller's control.
func buildTestPDFWith(t *testing.T, opts testPDFOptions) []byte {
	t.Helper()
	if opts.pages < 1 {
		t.Fatalf("a PDF needs at least one page, got %d", opts.pages)
	}
	pages, xrefStream := opts.pages, opts.xrefStream

	const (
		catalogID = 1
		pagesID   = 2
		fontID    = 3
		firstPage = 4
	)
	// Each page costs two objects: the page dictionary and its content stream.
	pageID := func(i int) int { return firstPage + 2*i }
	contentID := func(i int) int { return firstPage + 2*i + 1 }
	firstExtraID := contentID(pages-1) + 1

	var kids bytes.Buffer
	for i := 0; i < pages; i++ {
		fmt.Fprintf(&kids, " %d 0 R", pageID(i))
	}

	pageEntries := ""
	if opts.pageEntries != nil {
		pageEntries = " " + opts.pageEntries(firstExtraID)
	}

	bodies := map[int]string{
		catalogID: fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesID),
		pagesID: fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s ] /MediaBox [0 0 %d %d] >>",
			pages, kids.String(), testPageWidth, testPageHeight),
		fontID: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i := 0; i < pages; i++ {
		bodies[pageID(i)] = fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >>%s >>",
			pagesID, contentID(i), fontID, pageEntries)
		stream := fmt.Sprintf("BT /F1 18 Tf 72 %d Td (Page %d) Tj ET", testPageHeight-100, i+1)
		bodies[contentID(i)] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	}
	for i, body := range opts.extraObjects {
		bodies[firstExtraID+i] = body
	}

	lastID := contentID(pages-1) + len(opts.extraObjects)
	header := "%PDF-1.4\n"
	if xrefStream {
		header = "%PDF-1.5\n"
	}
	out := bytes.NewBufferString(header)
	offsets := make(map[int]int, lastID)
	for id := 1; id <= lastID; id++ {
		offsets[id] = out.Len()
		fmt.Fprintf(out, "%d 0 obj\n%s\nendobj\n", id, bodies[id])
	}

	if !xrefStream {
		start := out.Len()
		fmt.Fprintf(out, "xref\n0 %d\n", lastID+1)
		out.WriteString("0000000000 65535 f\r\n")
		for id := 1; id <= lastID; id++ {
			fmt.Fprintf(out, "%010d 00000 n\r\n", offsets[id])
		}
		fmt.Fprintf(out, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
			lastID+1, catalogID, start)
		return out.Bytes()
	}

	// The cross-reference stream is itself an object, so it indexes its own offset.
	xrefID := lastID + 1
	start := out.Len()
	var entries bytes.Buffer
	entry := func(kind byte, offset, gen int) {
		entries.WriteByte(kind)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(offset))
		entries.Write(b[:])
		var g [2]byte
		binary.BigEndian.PutUint16(g[:], uint16(gen))
		entries.Write(g[:])
	}
	entry(0, 0, 65535)
	for id := 1; id <= lastID; id++ {
		entry(1, offsets[id], 0)
	}
	entry(1, start, 0)

	var deflated bytes.Buffer
	w := zlib.NewWriter(&deflated)
	if _, err := w.Write(entries.Bytes()); err != nil {
		t.Fatalf("deflate xref stream: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("deflate xref stream: %v", err)
	}
	fmt.Fprintf(out, "%d 0 obj\n<< /Type /XRef /Size %d /W [ 1 4 2 ] /Root %d 0 R"+
		" /Filter /FlateDecode /Length %d >>\nstream\n", xrefID, xrefID+1, catalogID, deflated.Len())
	out.Write(deflated.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(out, "startxref\n%d\n%%%%EOF\n", start)
	return out.Bytes()
}
