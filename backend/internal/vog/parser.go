package vog

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/digitorus/pdf"
)

// Parse reads the fields a screening decision needs out of a VOG PDF. Justis
// PDFs may be encrypted (go-vog-issuer's reason for reaching for PDFium/WASM,
// #242); digitorus/pdf already supports that with an empty user password
// (NewReaderEncrypted), which is a lighter, already-vendored dependency (used
// elsewhere in this backend for PAdES signing) - reached for here first rather
// than adding a new WASM runtime.
//
// This has not been run against a real Justis VOG sample - none is available in
// this environment. The label matching below is deliberately tolerant (a label
// on its own line, or "label: value" on one line) precisely because the exact
// layout is unverified; verifying it against a real document is a gap this
// change flags rather than closes (see .ai/features/member-screening-vog.md).
func Parse(data []byte) (Document, error) {
	r, err := pdf.NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), func() string { return "" })
	if err != nil {
		return Document{}, fmt.Errorf("vog: open pdf: %w", err)
	}

	var lines []string
	for i := 1; i <= r.NumPage(); i++ {
		lines = append(lines, pageLines(r.Page(i))...)
	}
	return parseLines(lines)
}

// spaceGapPoints is how much horizontal gap between two glyphs counts as a word
// boundary, as a fraction of the current font size - the standard
// position-based-extraction heuristic (a genuine inter-word space advances the
// text position without producing its own glyph, per digitorus/pdf's
// showText: a space character is never emitted as a Text, only its width).
const spaceGapFraction = 0.2

// pageLines reconstructs reading-order lines of text from a page's positioned
// glyphs: group by Y (each glyph is its own Text - see the package doc above),
// sort left to right within a line, and insert a space wherever the horizontal
// gap between glyphs is wide enough to have been one.
func pageLines(p pdf.Page) []string {
	texts := append([]pdf.Text(nil), p.Content().Text...)
	sort.SliceStable(texts, func(i, j int) bool {
		if !closeEnough(texts[i].Y, texts[j].Y) {
			return texts[i].Y > texts[j].Y // top of page first
		}
		return texts[i].X < texts[j].X
	})

	var lines []string
	var cur strings.Builder
	var lastY, lastEndX float64
	open := false
	flush := func() {
		if line := strings.TrimSpace(cur.String()); line != "" {
			lines = append(lines, line)
		}
		cur.Reset()
	}
	for _, t := range texts {
		switch {
		case !open:
			open = true
		case !closeEnough(t.Y, lastY):
			flush()
		case t.X-lastEndX > spaceGapFraction*t.FontSize:
			cur.WriteString(" ")
		}
		cur.WriteString(t.S)
		lastY, lastEndX = t.Y, t.X+t.W
	}
	flush()
	return lines
}

func closeEnough(a, b float64) bool {
	d := a - b
	return d > -0.5 && d < 0.5
}

// codeLineRe matches a line that is entirely code-like tokens (digits, commas,
// separators) and nothing else - used to extend the "profiel:" code list onto a
// continuation line, stopping at the first line that looks like the next label.
var codeLineRe = regexp.MustCompile(`^[\d,;\s]+$`)

var codeRe = regexp.MustCompile(`\b\d{2}\b`)

func parseLines(lines []string) (Document, error) {
	kenmerk := fieldValue(lines, "kenmerk")
	if kenmerk == "" {
		return Document{}, ErrNotAVOG
	}

	given := fieldValue(lines, "Voornamen")
	prefix := fieldValue(lines, "Tussenvoegsels")
	surname := strings.TrimSpace(fieldValue(lines, "Geslachtsnaam"))
	if prefix != "" {
		surname = strings.TrimSpace(prefix + " " + surname)
	}

	dob, err := parseDutchDate(fieldValue(lines, "Geboortedatum"))
	if err != nil {
		return Document{}, fmt.Errorf("%w: geboortedatum: %v", ErrUnparseable, err)
	}
	issueDate, err := parseDutchDate(fieldValue(lines, "Datum"))
	if err != nil {
		return Document{}, fmt.Errorf("%w: datum: %v", ErrUnparseable, err)
	}

	var aspects, profiles []string
	for _, code := range profileCodes(lines) {
		if IsFunctionAspect(code) {
			aspects = append(aspects, code)
		} else {
			profiles = append(profiles, code)
		}
	}

	return Document{
		GivenNames:   given,
		Surname:      surname,
		DateOfBirth:  dob.Format("2006-01-02"),
		IssueDate:    issueDate,
		Reference:    kenmerk,
		AspectCodes:  aspects,
		ProfileCodes: profiles,
	}, nil
}

// fieldValue finds "label" as a whole line or a "label: value" line
// (case-insensitive) and returns the value - the text after the label on the
// same line, or, when that is empty, the next line (a label-only line followed
// by its value, the other common form layout).
func fieldValue(lines []string, label string) string {
	re := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(label) + `\s*:?\s*(.*)$`)
	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if v := strings.TrimSpace(m[1]); v != "" {
			return v
		}
		if i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	return ""
}

// profileCodes extracts every two-digit code following a "profiel:" label,
// continuing onto following lines for as long as they look code-only.
func profileCodes(lines []string) []string {
	re := regexp.MustCompile(`(?i)^\s*profiel\s*:?\s*(.*)$`)
	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		codes := codeRe.FindAllString(m[1], -1)
		for j := i + 1; j < len(lines) && codeLineRe.MatchString(lines[j]); j++ {
			codes = append(codes, codeRe.FindAllString(lines[j], -1)...)
		}
		return dedupe(codes)
	}
	return nil
}

func dedupe(codes []string) []string {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// dutchDateLayouts covers the numeric date formats a Justis PDF is expected to
// use; unverified against a real sample (see the package doc above).
var dutchDateLayouts = []string{"02-01-2006", "02/01/2006", "2006-01-02"}

func parseDutchDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty")
	}
	var lastErr error
	for _, layout := range dutchDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}
