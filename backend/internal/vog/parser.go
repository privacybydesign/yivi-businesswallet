package vog

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/pdftext"
)

// The parser is a port of go-vog-issuer's (#242), the implementation that was
// confirmed against real Justis documents. Two properties of those documents
// decide the approach: they are AES-encrypted (V=4/AESV2 with an empty user
// password, and a crypt-filter /Length written in bits, which digitorus/pdf's
// V4 check rejects - the reason the earlier digitorus/pdf-based parser failed
// every real VOG as "unparseable"), and their fields are laid out as columns
// (English label, Dutch label, value on one line) rather than "label: value"
// text. PDFium handles the encryption and exposes the position of every text
// run, which the layout-based extraction below relies on. It runs in a
// WebAssembly sandbox (wazero): pure Go, no cgo, no native library to ship.

const (
	// Pool sizing: one warm instance, at most four concurrent parses - a VOG
	// upload is rare and a parse takes well under a second, so anything more
	// only costs memory (each instance is a full PDFium heap).
	poolMinIdle  = 1
	poolMaxIdle  = 2
	poolMaxTotal = 4
	// instanceTimeout bounds how long a parse waits for a free instance when
	// all poolMaxTotal are busy.
	instanceTimeout = 30 * time.Second
)

// PDFiumParser reads the fields a screening decision needs out of a VOG PDF
// with PDFium running in a WebAssembly sandbox. Construct one per process with
// NewPDFiumParser (compiling the module takes seconds) and Close it on
// shutdown.
type PDFiumParser struct {
	pool pdfium.Pool
}

// NewPDFiumParser initialises the PDFium WebAssembly pool.
func NewPDFiumParser() (*PDFiumParser, error) {
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:  poolMinIdle,
		MaxIdle:  poolMaxIdle,
		MaxTotal: poolMaxTotal,
	})
	if err != nil {
		return nil, fmt.Errorf("vog: initialise pdfium: %w", err)
	}
	return &PDFiumParser{pool: pool}, nil
}

// Close releases the PDFium pool.
func (p *PDFiumParser) Close() error {
	return p.pool.Close()
}

// Parse extracts the screening-relevant fields from pdf. It returns ErrNotAVOG
// when pdf is not a readable PDF or does not carry a VOG's title,
// ErrUnparseable when it does but a required field could not be read, and a
// plain error for an infrastructure failure (no free PDFium instance, an
// extraction call failing) - the last is the caller's problem, not the
// document's, and must not be recorded against the member.
func (p *PDFiumParser) Parse(pdf []byte) (Document, error) {
	words, err := p.extractWords(pdf)
	if err != nil {
		return Document{}, err
	}
	return ExtractDocument(words)
}

func (p *PDFiumParser) extractWords(pdf []byte) ([]Word, error) {
	instance, err := p.pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("vog: get pdfium instance: %w", err)
	}
	defer func() {
		if err := instance.Close(); err != nil {
			slog.Warn("vog: return pdfium instance to pool", slog.String("error", err.Error()))
		}
	}()

	doc, err := instance.OpenDocument(&requests.OpenDocument{File: &pdf})
	if err != nil {
		return nil, fmt.Errorf("%w: open pdf: %v", ErrNotAVOG, err)
	}
	defer func() {
		if _, err := instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document}); err != nil {
			slog.Warn("vog: close pdf document", slog.String("error", err.Error()))
		}
	}()

	pageCount, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("vog: count pages: %w", err)
	}
	if pageCount.PageCount < 1 {
		return nil, fmt.Errorf("%w: pdf has no pages", ErrNotAVOG)
	}

	// Everything a decision needs is on the first page; the second carries
	// only the profile descriptions.
	text, err := instance.GetPageTextStructured(&requests.GetPageTextStructured{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}},
		Mode: requests.GetPageTextStructuredModeRects,
	})
	if err != nil {
		return nil, fmt.Errorf("vog: extract text: %w", err)
	}

	words := make([]Word, 0, len(text.Rects))
	for _, rect := range text.Rects {
		t := strings.TrimSpace(rect.Text)
		if t == "" {
			continue
		}
		words = append(words, Word{
			Text:   t,
			Left:   rect.PointPosition.Left,
			Top:    rect.PointPosition.Top,
			Right:  rect.PointPosition.Right,
			Bottom: rect.PointPosition.Bottom,
		})
	}
	return words, nil
}

// Word is a positioned piece of text on the first page of the VOG. Coordinates
// are PDF user space points: X grows to the right, Y grows upwards, so Top is
// larger than Bottom and the first line of the page has the largest Top.
type Word = pdftext.Word

// valueGap is how far (points) a word must start right of a label's end to be
// read as that label's value rather than as part of the label itself.
const valueGap = 2.0

// title is the document heading every VOG carries; its absence is what makes
// a validated PDF "not a VOG" rather than an unreadable one.
const title = "Verklaring Omtrent het Gedrag"

// profileMarker ends the sentence introducing the code list ("Er is bij deze
// screening uitgegaan van het volgende profiel:"); the codes follow on the
// next line(s) until the paragraph starting with profileEnd.
const (
	profileMarker = "profiel:"
	profileEnd    = "Op de volgende"
)

// fieldLabels maps the Dutch label printed on the VOG to the field it
// introduces. The English label precedes the Dutch one on the same line and the
// value follows the Dutch label, so the Dutch label is the anchor. Place and
// country of birth are on the document too but deliberately not read: a
// screening decision does not need them (#242's data-minimisation design).
var fieldLabels = map[string]string{
	"Datum":          "issue_date",
	"kenmerk":        "reference",
	"Geslachtsnaam":  "surname",
	"Tussenvoegsels": "prefix",
	"Voorna(a)m(en)": "given_names",
	"Geboortedatum":  "date_of_birth",
}

var codePattern = regexp.MustCompile(`\b\d{2}\b`)

// ExtractDocument interprets the positioned words of the first page of a VOG.
// It is layout based: labelled fields are read from the value column right of
// the Dutch label, and the profile codes are the line(s) after "profiel:".
// Exported so the pure layout logic is testable without PDFium.
func ExtractDocument(words []Word) (Document, error) {
	lines := pdftext.GroupLines(words)
	if len(lines) == 0 {
		return Document{}, fmt.Errorf("%w: no text found", ErrNotAVOG)
	}

	fullText := make([]string, len(lines))
	for i, l := range lines {
		fullText[i] = l.Text()
	}
	if !strings.Contains(strings.Join(fullText, "\n"), title) {
		return Document{}, fmt.Errorf("%w: title not found", ErrNotAVOG)
	}

	fields := labelledFields(lines)

	issueDate, err := ParseDutchDate(fields["issue_date"])
	if err != nil {
		return Document{}, fmt.Errorf("%w: datum: %v", ErrUnparseable, err)
	}
	dob, err := ParseDutchDate(fields["date_of_birth"])
	if err != nil {
		return Document{}, fmt.Errorf("%w: geboortedatum: %v", ErrUnparseable, err)
	}
	if fields["reference"] == "" || fields["surname"] == "" || fields["given_names"] == "" {
		return Document{}, fmt.Errorf("%w: kenmerk, geslachtsnaam or voornamen missing", ErrUnparseable)
	}

	codes := extractProfileCodes(fullText)
	if len(codes) == 0 {
		return Document{}, fmt.Errorf("%w: screening profile codes not found", ErrUnparseable)
	}
	aspects, profiles := splitCodes(codes)

	return Document{
		GivenNames:   fields["given_names"],
		Surname:      strings.TrimSpace(fields["prefix"] + " " + fields["surname"]),
		DateOfBirth:  dob.Format("2006-01-02"),
		IssueDate:    issueDate,
		Reference:    fields["reference"],
		AspectCodes:  aspects,
		ProfileCodes: profiles,
	}, nil
}

// labelledFields reads each labelled field's value: the words right of its
// label on the same line. The first label of a field wins.
func labelledFields(lines []pdftext.Line) map[string]string {
	fields := map[string]string{}
	for _, l := range lines {
		for i, w := range l.Words {
			field, ok := fieldLabels[w.Text]
			if !ok {
				continue
			}
			if _, seen := fields[field]; seen {
				continue
			}
			fields[field] = valueRightOf(w, l.Words[i+1:])
		}
	}
	return fields
}

// valueRightOf joins the words that start clear of label's end.
func valueRightOf(label Word, rest []Word) string {
	var value []string
	for _, v := range rest {
		if v.Left > label.Right+valueGap {
			value = append(value, v.Text)
		}
	}
	return strings.Join(value, " ")
}

// splitCodes splits the screening codes into function aspects and profiles.
func splitCodes(codes []string) (aspects, profiles []string) {
	for _, code := range codes {
		if IsFunctionAspect(code) {
			aspects = append(aspects, code)
		} else {
			profiles = append(profiles, code)
		}
	}
	return aspects, profiles
}

// extractProfileCodes returns the two digit codes on the line(s) following
// "...volgende profiel:" up to the next paragraph, deduplicated in order.
func extractProfileCodes(lines []string) []string {
	start := -1
	for i, l := range lines {
		if strings.Contains(l, profileMarker) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	seen := map[string]bool{}
	var codes []string
	for _, l := range lines[start:] {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, profileEnd) {
			break
		}
		for _, c := range codePattern.FindAllString(trimmed, -1) {
			if !seen[c] {
				seen[c] = true
				codes = append(codes, c)
			}
		}
	}
	return codes
}

// ParseDutchDate parses dates as printed on the VOG ("25 maart 2026"); the
// numeric forms "25-03-2026" and "2026-03-25" are accepted as well.
func ParseDutchDate(s string) (time.Time, error) {
	return pdftext.ParseDutchDate(s)
}
