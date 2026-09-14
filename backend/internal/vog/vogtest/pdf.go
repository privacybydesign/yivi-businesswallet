// Package vogtest builds minimal, valid, unencrypted single-page PDFs for
// exercising internal/vog.Parse and its consumers without a real Justis
// document (none is available in this environment to test against - see
// internal/vog/parser.go's doc comment).
package vogtest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// BuildTestPDF creates a PDF whose content stream draws each of lines as one Tj
// string, top to bottom. The font gives every glyph zero width except the space
// (a full em), so a gap-based word-boundary heuristic (internal/vog.Parse's)
// has an unambiguous signal regardless of how position-based extraction behaves
// for a real proportional font - this exercises the extraction and
// field-matching logic, not fidelity to a real Justis PDF.
func BuildTestPDF(t *testing.T, lines []string) []byte {
	t.Helper()

	var content strings.Builder
	content.WriteString("BT\n/F1 12 Tf\n72 750 Td\n")
	for i, line := range lines {
		if i > 0 {
			content.WriteString("0 -14 Td\n")
		}
		fmt.Fprintf(&content, "(%s) Tj\n", escapePDFString(line))
	}
	content.WriteString("ET\n")
	contentStr := content.String()

	widths := make([]string, 95) // FirstChar 32 .. LastChar 126
	for i := range widths {
		if i+32 == ' ' {
			widths[i] = "1000"
		} else {
			widths[i] = "0"
		}
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 6)

	writeObj := func(n int, body string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>")
	writeObj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(contentStr), contentStr))
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /FirstChar 32 /LastChar 126 "+
		"/Widths ["+strings.Join(widths, " ")+"] /Encoding /WinAnsiEncoding >>")

	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offsets))
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(offsets), xrefStart)

	return buf.Bytes()
}

func escapePDFString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}
