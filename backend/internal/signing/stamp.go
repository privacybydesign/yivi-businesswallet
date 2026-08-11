package signing

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/digitorus/pdf"
)

// A signer's paraphs cannot be part of their PAdES signature's own appearance: a
// signature dictionary carries exactly one appearance stream on one page, so
// initials on every page have nowhere to live inside it. They are therefore drawn
// as ordinary printable annotations in an incremental revision appended
// immediately BEFORE that signer's signature pass — which is what makes them
// attributable: the revision is inside the ByteRange their own signature covers,
// so the initials cannot be added, moved or removed without breaking it.
//
// Cost of that choice, stated where it is made: a signer with paraphs adds two
// revisions to the document instead of one, and the extra one is not itself a
// signing operation. Earlier co-signatures stay cryptographically valid (the
// revision is appended after their ByteRange), but a viewer that lists changes
// since a signature will name it. See .ai/features/signing-ceremony.md.

// paraphInk is the fill the paraph text is drawn in — the same ballpoint blue
// pdfsign draws a visible signature's name in, so the two marks on one page read
// as one hand rather than two tools.
const paraphInk = "0.2 0.2 0.6 rg"

// maxParaphChars bounds how much of a signer's name is drawn in a paraph box. A
// paraph is initials, and the box is a few millimetres wide, so a long name is
// initialised (see paraphText) rather than shrunk until it is a grey line.
const maxParaphChars = 6

// paraphText derives the mark drawn in a paraph box from the signer's name: their
// initials, which is what a paraph is. A name that is already short enough is used
// as it stands, so a two-letter name is not reduced to one.
func paraphText(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	if len([]rune(name)) <= maxParaphChars {
		return name
	}
	var initials []rune
	for _, part := range strings.Fields(name) {
		initials = append(initials, []rune(part)[0])
		if len(initials) == maxParaphChars {
			break
		}
	}
	if len(initials) == 0 {
		return "?"
	}
	return strings.ToUpper(string(initials))
}

// incrObject is one object of an appended revision: a new one, or a replacement for
// an object that already exists (a page, gaining an annotation). The two need no
// distinguishing — a cross-reference section names an object by id and offset, and an
// appended section that names an existing id is what replaces it.
type incrObject struct {
	id     uint32
	gen    uint16
	body   []byte
	offset int64 // filled in while writing
}

// stampMarks appends one incremental revision to input and returns the new
// document. The revision draws each paraph's initials and, when the signer placed
// one, their signature block, each as a printable, locked stamp annotation. Marks
// must already be validated against the document's geometry. With nothing to draw the
// input is returned untouched, so a signer with no placements adds no revision at all.
func stampMarks(input []byte, rdr *pdf.Reader, paraphs []Placement, paraphMark string, block *Placement, blockLines []string) ([]byte, error) {
	nextID := lastObjectID(rdr) + 1
	take := func() uint32 {
		id := nextID
		nextID++
		return id
	}

	var objects []incrObject
	// One appearance + one annotation per mark, collected per page so each page
	// object is rewritten once however many marks land on it.
	annotsByPage := make(map[int][]uint32, len(paraphs)+1)
	pageOrder := make([]int, 0, len(paraphs)+1)
	queue := func(page int, annotID uint32) {
		if _, seen := annotsByPage[page]; !seen {
			pageOrder = append(pageOrder, page)
		}
		annotsByPage[page] = append(annotsByPage[page], annotID)
	}

	if len(paraphs) > 0 || block != nil {
		fontID := take()
		objects = append(objects, incrObject{id: fontID, body: paraphFont()})

		for _, m := range paraphs {
			appearanceID, annotID := take(), take()
			pageRef, err := pageReference(rdr, m.Page)
			if err != nil {
				return nil, err
			}
			objects = append(objects,
				incrObject{id: appearanceID, body: paraphAppearance(m, paraphMark, fontID)},
				incrObject{id: annotID, body: stampAnnotation(m, paraphMark, appearanceID, pageRef)},
			)
			queue(m.Page, annotID)
		}

		if block != nil {
			appearanceID, annotID := take(), take()
			pageRef, err := pageReference(rdr, block.Page)
			if err != nil {
				return nil, err
			}
			objects = append(objects,
				incrObject{id: appearanceID, body: signatureBlockAppearance(*block, blockLines, fontID)},
				incrObject{id: annotID, body: stampAnnotation(*block, strings.Join(blockLines, " "), appearanceID, pageRef)},
			)
			queue(block.Page, annotID)
		}
	}

	if len(objects) == 0 {
		return input, nil
	}

	for _, page := range pageOrder {
		pageObj, err := rewritePage(rdr, page, annotsByPage[page])
		if err != nil {
			return nil, err
		}
		objects = append(objects, pageObj)
	}

	return appendRevision(input, rdr, objects, nextID)
}

// lastObjectID reports the highest object id the document already uses, so the
// appended revision can number its objects above it.
func lastObjectID(rdr *pdf.Reader) uint32 {
	var maxID uint32
	for _, entry := range rdr.Xref() {
		ptr := entry.Ptr()
		if id := ptr.GetID(); id > maxID {
			maxID = id
		}
	}
	return maxID
}

// paraphFont is the appearance streams' shared font. It matches the font pdfsign
// uses for a visible signature's name, for the same reason the ink does.
//
// The encoding is stated rather than left to default: without it the bytes of the
// content stream are read in Adobe StandardEncoding, in which the UTF-8 of any name
// with a diacritic draws as something else entirely. WinAnsiEncoding covers the
// accented Latin letters a Dutch or EU name is made of; winAnsi does the transcode.
func paraphFont() []byte {
	return []byte("<<\n  /Type /Font\n  /Subtype /Type1\n  /BaseFont /Times-Roman\n" +
		"  /Encoding /WinAnsiEncoding\n>>")
}

// winAnsiUnmappable is drawn for a character WinAnsiEncoding has no place for — a
// paraph in a non-Latin script. A defined substitute is the honest outcome: the
// alternative is a notdef box or, worse, a different letter.
const winAnsiUnmappable = '?'

// winAnsiHigh maps the characters WinAnsiEncoding puts in 0x80-0x9F, where it and
// Latin-1 disagree. Everything else it encodes is either ASCII or at its Unicode
// code point (PDF 32000-1 annex D.2).
var winAnsiHigh = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85,
	'†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A,
	'‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E, '‘': 0x91, '’': 0x92,
	'“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C,
	'ž': 0x9E, 'Ÿ': 0x9F,
}

// winAnsi encodes text for a font declaring /WinAnsiEncoding: one byte per drawn
// glyph, which is also what the box is measured in.
func winAnsi(text string) []byte {
	out := make([]byte, 0, len(text))
	for _, r := range text {
		switch {
		case r >= 0x20 && r < 0x7F:
			out = append(out, byte(r))
		case r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			if b, ok := winAnsiHigh[r]; ok {
				out = append(out, b)
				continue
			}
			out = append(out, winAnsiUnmappable)
		}
	}
	return out
}

// paraphAppearance is the Form XObject drawn inside the paraph's rectangle. Its
// coordinate space is the rectangle itself (origin at its lower-left corner), so
// the text is positioned without reference to where on the page the box sits.
func paraphAppearance(m Placement, text string, fontID uint32) []byte {
	drawn := winAnsi(text)
	fontSize, x, y := paraphTextLayout(len(drawn), m.Width, m.Height)
	var stream bytes.Buffer
	stream.WriteString("q\nBT\n")
	fmt.Fprintf(&stream, "/F1 %.2f Tf\n", fontSize)
	fmt.Fprintf(&stream, "%.2f %.2f Td\n", x, y)
	stream.WriteString(paraphInk + "\n")
	fmt.Fprintf(&stream, "%s Tj\n", pdfLiteral(drawn))
	stream.WriteString("ET\nQ")

	var obj bytes.Buffer
	obj.WriteString("<<\n  /Type /XObject\n  /Subtype /Form\n  /FormType 1\n")
	fmt.Fprintf(&obj, "  /BBox [0 0 %.2f %.2f]\n", m.Width, m.Height)
	obj.WriteString("  /Matrix [1 0 0 1 0 0]\n")
	fmt.Fprintf(&obj, "  /Resources << /Font << /F1 %d 0 R >> >>\n", fontID)
	fmt.Fprintf(&obj, "  /Length %d\n>>\nstream\n", stream.Len())
	obj.Write(stream.Bytes())
	obj.WriteString("\nendstream")
	return obj.Bytes()
}

// paraphTextLayout sizes the paraph text to its box and centres it, the same
// approximation pdfsign uses for a signature appearance (Times-Roman averages
// about half its point size per character). It counts the glyphs the content stream
// actually draws — one per encoded byte — not the runes they came from.
func paraphTextLayout(glyphs int, width, height float64) (fontSize, x, y float64) {
	const averageGlyphWidth = 0.5
	chars := float64(glyphs)
	fontSize = height * 0.8
	if chars*fontSize*averageGlyphWidth > width {
		fontSize = width / (chars * averageGlyphWidth)
	}
	x = max((width-chars*fontSize*averageGlyphWidth)/2, 0)
	y = (height-fontSize)/2 + fontSize/3
	return fontSize, x, y
}

// signatureBlockPadding is the inset, in points, between the block's rectangle and
// its text, so the lines do not sit flush against the annotation's edge.
const signatureBlockPadding = 3

// signatureLinePitch is the baseline-to-baseline distance as a multiple of the font
// size, giving the stacked lines a little air.
const signatureLinePitch = 1.25

// signatureBlockAppearance is the Form XObject drawn inside a signer's signature
// block: their lines top-to-bottom, left-aligned, in the same font and ink the
// paraphs use so the two marks read as one hand. Its coordinate space is the
// rectangle itself (origin at the lower-left corner), so the text is positioned
// without reference to where on the page the box sits.
func signatureBlockAppearance(m Placement, lines []string, fontID uint32) []byte {
	drawn := make([][]byte, len(lines))
	widest := 0
	for i, line := range lines {
		drawn[i] = winAnsi(line)
		if len(drawn[i]) > widest {
			widest = len(drawn[i])
		}
	}
	fontSize, x, top, pitch := signatureTextLayout(len(drawn), widest, m.Width, m.Height)

	var stream bytes.Buffer
	stream.WriteString("q\nBT\n")
	fmt.Fprintf(&stream, "/F1 %.2f Tf\n", fontSize)
	stream.WriteString(paraphInk + "\n")
	fmt.Fprintf(&stream, "%.2f %.2f Td\n", x, top)
	for i, line := range drawn {
		if i > 0 {
			// Td is relative in text space, so each line steps down from the last.
			fmt.Fprintf(&stream, "0 %.2f Td\n", -pitch)
		}
		fmt.Fprintf(&stream, "%s Tj\n", pdfLiteral(line))
	}
	stream.WriteString("ET\nQ")

	var obj bytes.Buffer
	obj.WriteString("<<\n  /Type /XObject\n  /Subtype /Form\n  /FormType 1\n")
	fmt.Fprintf(&obj, "  /BBox [0 0 %.2f %.2f]\n", m.Width, m.Height)
	obj.WriteString("  /Matrix [1 0 0 1 0 0]\n")
	fmt.Fprintf(&obj, "  /Resources << /Font << /F1 %d 0 R >> >>\n", fontID)
	fmt.Fprintf(&obj, "  /Length %d\n>>\nstream\n", stream.Len())
	obj.Write(stream.Bytes())
	obj.WriteString("\nendstream")
	return obj.Bytes()
}

// signatureTextLayout sizes a signature block's lines to its rectangle: a font size
// that stacks every line at signatureLinePitch within the height and keeps the widest
// line within the width (Times-Roman averages about half its point size per glyph,
// the approximation paraphs use). It returns the size, the left inset, the first
// baseline and the baseline-to-baseline pitch. Glyphs are counted as encoded bytes,
// which is what the content stream draws.
func signatureTextLayout(lineCount, widestGlyphs int, width, height float64) (fontSize, x, top, pitch float64) {
	const averageGlyphWidth = 0.5
	availWidth := max(width-2*signatureBlockPadding, 1)
	availHeight := max(height-2*signatureBlockPadding, 1)
	lines := float64(max(lineCount, 1))
	fontSize = availHeight / (lines * signatureLinePitch)
	if glyphs := float64(widestGlyphs); glyphs > 0 {
		if byWidth := availWidth / (glyphs * averageGlyphWidth); byWidth < fontSize {
			fontSize = byWidth
		}
	}
	pitch = fontSize * signatureLinePitch
	x = signatureBlockPadding
	top = height - signatureBlockPadding - fontSize
	return fontSize, x, top, pitch
}

// Annotation flags (PDF 32000-1 table 165): the mark prints, and is read-only and
// locked so a reader cannot drag it off its rectangle or delete it — the signature
// covering it would then no longer verify, which is a worse way to find out than not
// being offered the handle.
const stampAnnotationFlags = 4 | 64 | 128

// stampAnnotation is the annotation that puts a mark's appearance on the page. It
// serves both a paraph and a signer's signature block: the two differ only in what
// their appearance stream draws, not in how the annotation is attached or locked.
func stampAnnotation(m Placement, text string, appearanceID uint32, pageRef string) []byte {
	r := m.rect()
	var obj bytes.Buffer
	obj.WriteString("<<\n  /Type /Annot\n  /Subtype /Stamp\n")
	fmt.Fprintf(&obj, "  /Rect [%.2f %.2f %.2f %.2f]\n", r[0], r[1], r[2], r[3])
	fmt.Fprintf(&obj, "  /F %d\n", stampAnnotationFlags)
	fmt.Fprintf(&obj, "  /AP << /N %d 0 R >>\n", appearanceID)
	// /Contents is a PDF text string, which is a different encoding from the
	// appearance stream's bytes: UTF-16BE with a byte-order mark carries any name.
	fmt.Fprintf(&obj, "  /Contents %s\n", pdfTextString(text))
	fmt.Fprintf(&obj, "  /P %s\n", pageRef)
	obj.WriteString(">>")
	return obj.Bytes()
}

// pageReference is the "id gen R" reference to a page object. A page that is not an
// indirect object cannot be referenced — nor replaced by an incremental revision, so
// its paraphs could not be attached either.
func pageReference(rdr *pdf.Reader, number int) (string, error) {
	page := rdr.Page(number)
	if page.V.IsNull() {
		return "", ErrInvalidPDF
	}
	ptr := page.V.GetPtr()
	if ptr.GetID() == 0 {
		return "", ErrInvalidPDF
	}
	return fmt.Sprintf("%d %d R", ptr.GetID(), ptr.GetGen()), nil
}

// rewritePage rebuilds one page dictionary with extra annotation references
// appended to its /Annots, as an object the revision can put in the page's place. The
// page keeps its own object number and generation, and every reference is written
// back as-is, so a producer that reused object numbers is preserved exactly.
//
// The dictionary is rebuilt key by key rather than copied byte for byte because
// digitorus/pdf resolves as it reads and does not hand back the raw object. What it
// hands back instead is written out by writeValue, which emits only forms that are
// valid PDF — never Value.String(), which is a debug formatter (a stream comes out as
// `<<...>>@offset` and a string Go-quoted, and neither parses). References are
// followed by writing the reference, not what it points at, so the rest of the file
// is not inlined.
func rewritePage(rdr *pdf.Reader, number int, extra []uint32) (incrObject, error) {
	page := rdr.Page(number)
	if page.V.IsNull() {
		return incrObject{}, ErrInvalidPDF
	}
	ptr := page.V.GetPtr()
	if ptr.GetID() == 0 {
		return incrObject{}, ErrInvalidPDF
	}

	var buf bytes.Buffer
	buf.WriteString("<<\n")
	for _, key := range page.V.Keys() {
		if key == "Annots" {
			continue // written below, with the new annotations appended
		}
		buf.WriteString("  " + pdfName(key) + " ")
		if err := writeValue(&buf, page.V.Key(key), page.V, 0); err != nil {
			return incrObject{}, err
		}
		buf.WriteString("\n")
	}

	annots := page.V.Key("Annots")
	if !annots.IsNull() || len(extra) > 0 {
		buf.WriteString("  /Annots [")
		for i := 0; i < annots.Len(); i++ {
			// writeValue writes a reference entry as "id gen R" and a directly-embedded
			// annotation dictionary inline — both valid, and both kept as they were.
			buf.WriteString(" ")
			if err := writeValue(&buf, annots.Index(i), annots, 0); err != nil {
				return incrObject{}, err
			}
		}
		for _, id := range extra {
			fmt.Fprintf(&buf, " %d 0 R", id)
		}
		buf.WriteString(" ]\n")
	}
	buf.WriteString(">>")

	return incrObject{id: ptr.GetID(), gen: ptr.GetGen(), body: buf.Bytes()}, nil
}

// maxValueDepth bounds how far into a page entry these walks go. Nothing in a page
// dictionary is legitimately this nested, and a document whose page dictionary holds
// a direct reference to itself would otherwise recurse until the stack gives out —
// which, unlike digitorus/pdf's panics, no recover() can catch.
const maxValueDepth = 32

// pdfRef reports the "id gen R" reference a value was read from, and whether it was
// read from one at all. digitorus/pdf resolves a reference as it reads (both
// Value.Key and Value.Index go through Reader.resolve), and the resolved value
// carries the object it came from, while a value written out directly carries its
// container's instead. Comparing the two is the only way back to "was this a
// reference", and it is what lets a page be rebuilt without inlining half the file.
func pdfRef(value, container pdf.Value) (string, bool) {
	ptr, own := value.GetPtr(), container.GetPtr()
	id, gen := ptr.GetID(), ptr.GetGen()
	if id == 0 || (id == own.GetID() && gen == own.GetGen()) {
		return "", false
	}
	return fmt.Sprintf("%d %d R", id, gen), true
}

// writeValue writes value back as PDF syntax, following references by writing the
// reference rather than what it points at. Anything it cannot represent is an error
// rather than a guess: this runs over a document that is about to be signed.
func writeValue(buf *bytes.Buffer, value, container pdf.Value, depth int) error {
	if depth > maxValueDepth {
		return ErrInvalidPDF
	}
	if ref, ok := pdfRef(value, container); ok {
		buf.WriteString(ref)
		return nil
	}
	switch value.Kind() {
	case pdf.Null:
		buf.WriteString("null")
	case pdf.Bool:
		fmt.Fprintf(buf, "%t", value.Bool())
	case pdf.Integer:
		fmt.Fprintf(buf, "%d", value.Int64())
	case pdf.Real:
		buf.WriteString(strconv.FormatFloat(value.Float64(), 'f', -1, 64))
	case pdf.Name:
		buf.WriteString(pdfName(value.Name()))
	case pdf.String:
		// A hex string carries any byte sequence and needs no escaping, so it is the
		// one form a string read back out of a document always survives.
		fmt.Fprintf(buf, "<%X>", value.RawString())
	case pdf.Array:
		buf.WriteString("[")
		for i := 0; i < value.Len(); i++ {
			if i > 0 {
				buf.WriteString(" ")
			}
			if err := writeValue(buf, value.Index(i), value, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString("]")
	case pdf.Dict:
		buf.WriteString("<<")
		for _, key := range value.Keys() {
			buf.WriteString(" " + pdfName(key) + " ")
			if err := writeValue(buf, value.Key(key), value, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString(" >>")
	default:
		// A stream is an indirect object, so one reached here was written directly
		// into a dictionary: not representable, and not a document to sign blind.
		return ErrInvalidPDF
	}
	return nil
}

// pdfName writes a name back, escaping everything a name may not carry directly
// (PDF 32000-1 §7.3.5). digitorus/pdf hands names back decoded, so a name that
// arrived escaped has to be escaped again.
func pdfName(name string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, c := range []byte(name) {
		if c < '!' || c > '~' || strings.IndexByte("()<>[]{}/%#", c) >= 0 {
			fmt.Fprintf(&b, "#%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// appendRevision writes objects as one incremental revision on the end of input:
// the objects, a cross-reference section of the same kind the document already
// uses, and a trailer pointing back at the previous section. nextID is the first
// object id the document does not use yet — an xref stream is itself an object of
// the revision it indexes, so it needs one of its own.
func appendRevision(input []byte, rdr *pdf.Reader, objects []incrObject, nextID uint32) ([]byte, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(input)+revisionSizeGuess(objects)))
	out.Write(input)
	if !bytes.HasSuffix(input, []byte("\n")) {
		out.WriteString("\n")
	}

	for i := range objects {
		objects[i].offset = int64(out.Len())
		fmt.Fprintf(out, "%d %d obj\n", objects[i].id, objects[i].gen)
		out.Write(objects[i].body)
		out.WriteString("\nendobj\n")
	}

	if rdr.XrefInformation.Type == "stream" {
		return appendXrefStream(out, rdr, objects, nextID)
	}
	return appendXrefTable(out, rdr, objects)
}

// revisionSizeGuess pre-sizes the output buffer so appending a revision to a
// multi-megabyte upload does not repeatedly copy it.
func revisionSizeGuess(objects []incrObject) int {
	const perObjectOverhead = 64
	total := 512
	for _, o := range objects {
		total += len(o.body) + perObjectOverhead
	}
	return total
}

func appendXrefTable(out *bytes.Buffer, rdr *pdf.Reader, objects []incrObject) ([]byte, error) {
	xrefStart := out.Len()
	out.WriteString("xref\n")
	// One subsection per object: the ids of an incremental revision are only
	// contiguous by accident (the updated pages are not), and per-object subsections
	// are always correct.
	for _, o := range objects {
		fmt.Fprintf(out, "%d 1\n", o.id)
		fmt.Fprintf(out, "%010d %05d n\r\n", o.offset, o.gen)
	}
	out.WriteString("trailer\n<<\n")
	fmt.Fprintf(out, "  /Size %d\n", trailerSize(rdr, objects, 0))
	root, err := rootReference(rdr)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "  /Root %s\n", root)
	if id := documentID(rdr); id != "" {
		fmt.Fprintf(out, "  /ID %s\n", id)
	}
	fmt.Fprintf(out, "  /Prev %d\n>>\n", rdr.XrefInformation.StartPos)
	fmt.Fprintf(out, "startxref\n%d\n%%%%EOF\n", xrefStart)
	return out.Bytes(), nil
}

// appendXrefStream writes the revision's cross-reference as an xref stream, which
// is what a document that already uses one needs: a classic table would leave a
// reader that found this section first with no way to read the object streams the
// previous section indexes.
func appendXrefStream(out *bytes.Buffer, rdr *pdf.Reader, objects []incrObject, streamID uint32) ([]byte, error) {
	// The stream indexes itself, so its own offset has to be known before its
	// entries are encoded — it is written at the current end of the buffer.
	xrefStart := out.Len()

	// /W [1 4 2]: one type byte, a four-byte offset, a two-byte generation.
	var entries bytes.Buffer
	writeEntry := func(offset int64, gen uint16) {
		entries.WriteByte(1)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(offset))
		entries.Write(b[:])
		var g [2]byte
		binary.BigEndian.PutUint16(g[:], gen)
		entries.Write(g[:])
	}
	var index bytes.Buffer
	for _, o := range objects {
		fmt.Fprintf(&index, " %d 1", o.id)
		writeEntry(o.offset, o.gen)
	}
	fmt.Fprintf(&index, " %d 1", streamID)
	writeEntry(int64(xrefStart), 0)

	var deflated bytes.Buffer
	w := zlib.NewWriter(&deflated)
	if _, err := w.Write(entries.Bytes()); err != nil {
		return nil, fmt.Errorf("signing: encode xref stream: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("signing: encode xref stream: %w", err)
	}

	root, err := rootReference(rdr)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "%d 0 obj\n<<\n  /Type /XRef\n", streamID)
	fmt.Fprintf(out, "  /Size %d\n", trailerSize(rdr, objects, streamID))
	fmt.Fprintf(out, "  /Index [%s ]\n", index.String())
	out.WriteString("  /W [ 1 4 2 ]\n")
	fmt.Fprintf(out, "  /Root %s\n", root)
	if id := documentID(rdr); id != "" {
		fmt.Fprintf(out, "  /ID %s\n", id)
	}
	fmt.Fprintf(out, "  /Prev %d\n", rdr.XrefInformation.StartPos)
	out.WriteString("  /Filter /FlateDecode\n")
	fmt.Fprintf(out, "  /Length %d\n>>\nstream\n", deflated.Len())
	out.Write(deflated.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(out, "startxref\n%d\n%%%%EOF\n", xrefStart)
	return out.Bytes(), nil
}

// trailerSize is the /Size the appended section reports: one past the highest object
// id in the file. It never goes below the /Size the document already declared — a
// document may reserve more object numbers than it uses, and a reader that trusts a
// shrunken /Size would stop seeing the objects above it.
func trailerSize(rdr *pdf.Reader, objects []incrObject, extra uint32) uint32 {
	maxID := extra
	for _, o := range objects {
		if o.id > maxID {
			maxID = o.id
		}
	}
	size := maxID + 1
	//nolint:gosec // A PDF object count does not reach 2^31; a negative one is ignored.
	if declared := uint32(rdr.Trailer().Key("Size").Int64()); declared > size {
		return declared
	}
	return size
}

// rootReference is the reference to the document catalogue. An appended section
// carries its own trailer, so it has to name the catalogue again.
func rootReference(rdr *pdf.Reader) (string, error) {
	ptr := rdr.Trailer().Key("Root").GetPtr()
	if ptr.GetID() == 0 {
		return "", ErrInvalidPDF
	}
	return fmt.Sprintf("%d %d R", ptr.GetID(), ptr.GetGen()), nil
}

// documentID re-states the file identifier on the appended section, so the two
// halves of the revision chain agree about which document this is. It is written
// back as hex strings, which is the only form a binary identifier survives.
func documentID(rdr *pdf.Reader) string {
	id := rdr.Trailer().Key("ID")
	if id.Kind() != pdf.Array || id.Len() != 2 {
		return ""
	}
	return fmt.Sprintf("[<%X><%X>]", id.Index(0).RawString(), id.Index(1).RawString())
}

// pdfLiteral escapes already-encoded bytes as a PDF literal string. Only the three
// characters that can end or nest one need escaping.
func pdfLiteral(encoded []byte) string {
	var b strings.Builder
	b.WriteByte('(')
	for _, c := range encoded {
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte(')')
	return b.String()
}

// pdfTextString writes text as a PDF text string (PDF 32000-1 §7.9.2.2): UTF-16BE
// behind a byte-order mark, as a hex string, which needs no escaping and carries a
// name from any script.
func pdfTextString(text string) string {
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, unit := range utf16.Encode([]rune(text)) {
		fmt.Fprintf(&b, "%04X", unit)
	}
	b.WriteByte('>')
	return b.String()
}
