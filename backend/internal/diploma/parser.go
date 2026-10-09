package diploma

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
)

// ErrNotADiploma is returned when the PDF does not look like a DUO diploma
// extract at all.
var ErrNotADiploma = errors.New("document is not a DUO diploma extract")

// Parser extracts the printed data from a diploma extract PDF.
type Parser interface {
	Parse(ctx context.Context, pdf []byte) (*Document, error)
}

// Word is a positioned piece of text on a page. Coordinates are PDF user
// space points: X grows to the right, Y grows upwards, so Top is larger than
// Bottom and the first line of the page has the largest Top.
type Word struct {
	Text   string
	Left   float64
	Top    float64
	Right  float64
	Bottom float64
}

const (
	// pdfiumInstanceTimeout bounds how long a parse waits for a free instance.
	pdfiumInstanceTimeout = 30 * time.Second
	// pdfiumParseTimeout bounds one parse once it has an instance. A genuine
	// extract parses in well under a second; past this the instance is killed,
	// so a crafted PDF cannot hold one of the pool's few instances indefinitely.
	pdfiumParseTimeout = 20 * time.Second
	// pdfiumMaxParses is how many of the borrowed pool's instances diploma
	// parses may hold at once: fewer than the pool has (vog's poolMaxTotal is
	// 4), so a burst of diploma uploads cannot starve VOG uploads.
	pdfiumMaxParses = 2
)

// PDFiumParser parses diploma extracts with PDFium running in a WebAssembly
// sandbox (pure Go, no cgo). It borrows a pool another parser owns (the VOG
// parser's, vog.PDFiumParser.Pool): a pool is a full PDFium heap per
// instance, so a process keeps one.
type PDFiumParser struct {
	pool pdfium.Pool
	// parses holds a slot per parse in flight, at most pdfiumMaxParses.
	parses chan struct{}
}

// NewPDFiumParser parses on pool, which its owner closes.
func NewPDFiumParser(pool pdfium.Pool) *PDFiumParser {
	return &PDFiumParser{pool: pool, parses: make(chan struct{}, pdfiumMaxParses)}
}

// Pages is the text of an extract: the positioned words of the first page and
// the plain text of the remaining pages.
type Pages struct {
	First []Word
	Rest  []string
}

// Parse extracts the diploma data from the PDF bytes.
func (p *PDFiumParser) Parse(ctx context.Context, pdf []byte) (*Document, error) {
	pages, err := p.extract(ctx, pdf)
	if err != nil {
		return nil, err
	}
	return extractDocument(pages)
}

// extract reads the pages on a pooled instance, killing the instance when the
// parse outlives pdfiumParseTimeout or ctx. Kill interrupts the WebAssembly
// call in flight only because the pool's runtime closes on context done
// (vog.NewPDFiumParser).
func (p *PDFiumParser) extract(ctx context.Context, pdf []byte) (*Pages, error) {
	wait := time.NewTimer(pdfiumInstanceTimeout)
	defer wait.Stop()
	select {
	case p.parses <- struct{}{}:
		defer func() { <-p.parses }()
	case <-wait.C:
		return nil, errors.New("diploma: get pdfium instance: too many parses in flight")
	case <-ctx.Done():
		return nil, fmt.Errorf("diploma: get pdfium instance: %w", ctx.Err())
	}

	instance, err := p.pool.GetInstance(pdfiumInstanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("diploma: get pdfium instance: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, pdfiumParseTimeout)
	defer cancel()

	type result struct {
		pages *Pages
		err   error
	}
	done := make(chan result, 1)
	go func() {
		pages, err := readPages(instance, pdf)
		done <- result{pages, err}
	}()
	select {
	case r := <-done:
		if err := instance.Close(); err != nil {
			slog.Warn("diploma: return pdfium instance to pool", slog.String("error", err.Error()))
		}
		return r.pages, r.err
	case <-ctx.Done():
		if err := instance.Kill(); err != nil {
			slog.Warn("diploma: kill pdfium instance", slog.String("error", err.Error()))
		}
		return nil, fmt.Errorf("diploma: parse pdf: %w", ctx.Err())
	}
}

func readPages(instance pdfium.Pdfium, pdf []byte) (*Pages, error) {
	doc, err := instance.OpenDocument(&requests.OpenDocument{File: &pdf})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to open pdf: %v", ErrNotADiploma, err)
	}
	defer func() {
		_, err := instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
		if err != nil {
			slog.Warn("diploma: close pdf document", slog.String("error", err.Error()))
		}
	}()

	pageCount, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("diploma: count pages: %w", err)
	}
	if pageCount.PageCount < 1 {
		return nil, fmt.Errorf("%w: pdf has no pages", ErrNotADiploma)
	}

	text, err := instance.GetPageTextStructured(&requests.GetPageTextStructured{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}},
		Mode: requests.GetPageTextStructuredModeRects,
	})
	if err != nil {
		return nil, fmt.Errorf("diploma: extract text: %w", err)
	}
	pages := &Pages{First: make([]Word, 0, len(text.Rects))}
	for _, rect := range text.Rects {
		t := strings.TrimSpace(rect.Text)
		if t == "" {
			continue
		}
		pages.First = append(pages.First, Word{
			Text:   t,
			Left:   rect.PointPosition.Left,
			Top:    rect.PointPosition.Top,
			Right:  rect.PointPosition.Right,
			Bottom: rect.PointPosition.Bottom,
		})
	}

	for i := 1; i < pageCount.PageCount; i++ {
		pageText, err := instance.GetPageText(&requests.GetPageText{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}},
		})
		if err != nil {
			return nil, fmt.Errorf("diploma: extract text of page %d: %w", i+1, err)
		}
		pages.Rest = append(pages.Rest, pageText.Text)
	}
	return pages, nil
}

// line is a group of words that share (approximately) the same baseline.
type line struct {
	top   float64
	words []Word
}

func (l line) text() string {
	parts := make([]string, len(l.words))
	for i, w := range l.words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// lineTolerance is the maximum vertical distance (points) between two words on
// the same line.
const lineTolerance = 4.0

func groupLines(words []Word) []line {
	sorted := make([]Word, len(words))
	copy(sorted, words)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Top != sorted[j].Top {
			return sorted[i].Top > sorted[j].Top
		}
		return sorted[i].Left < sorted[j].Left
	})

	var lines []line
	for _, w := range sorted {
		if len(lines) > 0 {
			current := &lines[len(lines)-1]
			if current.top-w.Top <= lineTolerance {
				current.words = append(current.words, w)
				continue
			}
		}
		lines = append(lines, line{top: w.Top, words: []Word{w}})
	}
	for i := range lines {
		sort.Slice(lines[i].words, func(a, b int) bool {
			return lines[i].words[a].Left < lines[i].words[b].Left
		})
	}
	return lines
}

// Headings printed on the extract, in the order they appear.
const (
	headingRegister  = "Uit het register onderwijsdeelnemers"
	headingProfile   = "PROFIEL"
	headingAchieved  = "BEHAALD DOOR"
	headingIssued    = "UITGEGEVEN DOOR"
	labelBorn        = "Geboren op"
	labelDownload    = "Downloaddatum:"
	headingGradeList = "CIJFERLIJST"
)

// documentTypes are the headings that introduce the qualification.
var documentTypes = map[string]string{
	"DIPLOMA":       "Diploma",
	"GETUIGSCHRIFT": "Getuigschrift",
	"CERTIFICAAT":   "Certificaat",
}

var (
	rePlaceDate = regexp.MustCompile(`^(.+?),\s*(\d{1,2} [A-Za-z]+ \d{4})$`)
	reLevel     = regexp.MustCompile(`NLQF\s*(\d+\+?)\s*/\s*EQF\s*(\d+\+?)`)
	reFooter    = regexp.MustCompile(`(\d+)\s*-\s*pagina\s*(\d+)\s*/\s*(\d+)`)
)

// extractDocument interprets the text of a diploma extract. It is layout
// based: the extract is a sequence of headings ("DIPLOMA", "BEHAALD DOOR",
// "UITGEGEVEN DOOR") each followed by the lines that belong to it.
func extractDocument(pages *Pages) (*Document, error) {
	lines := groupLines(pages.First)
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: no text found", ErrNotADiploma)
	}
	texts := make([]string, 0, len(lines))
	for _, l := range lines {
		if t := strings.TrimSpace(l.text()); t != "" {
			texts = append(texts, t)
		}
	}
	if !containsLine(texts, headingRegister) {
		return nil, fmt.Errorf("%w: register heading not found", ErrNotADiploma)
	}

	doc := &Document{Pages: 1 + len(pages.Rest), Profiles: []string{}}

	// Split the page into sections keyed by heading.
	type section struct {
		heading string
		lines   []string
	}
	var sections []section
	for _, t := range texts {
		if title, ok := documentTypes[t]; ok {
			doc.DocumentType = title
			sections = append(sections, section{heading: "TYPE"})
			continue
		}
		switch t {
		case headingRegister, headingProfile, headingAchieved, headingIssued:
			sections = append(sections, section{heading: t})
			continue
		}
		if len(sections) == 0 {
			continue
		}
		current := &sections[len(sections)-1]
		current.lines = append(current.lines, t)
	}

	var err error
	for _, s := range sections {
		switch s.heading {
		case "TYPE":
			doc.Qualification = strings.Join(s.lines, " ")
		case headingProfile:
			for _, l := range s.lines {
				profile := strings.TrimSpace(strings.TrimLeft(l, "•-* "))
				profile = strings.TrimSpace(strings.TrimPrefix(profile, "Profiel"))
				if profile != "" {
					doc.Profiles = append(doc.Profiles, profile)
				}
			}
		case headingAchieved:
			var name []string
			for _, l := range s.lines {
				if strings.HasPrefix(l, labelBorn) {
					if doc.DateOfBirth, err = parseDutchDate(strings.TrimSpace(strings.TrimPrefix(l, labelBorn))); err != nil {
						return nil, fmt.Errorf("%w: date of birth: %v", ErrNotADiploma, err)
					}
					continue
				}
				name = append(name, l)
			}
			doc.FullName = strings.Join(name, " ")
		case headingIssued:
			var institution []string
			for _, l := range s.lines {
				if doc.DateAwarded.IsZero() {
					if m := rePlaceDate.FindStringSubmatch(l); m != nil {
						if t, err := parseDutchDate(m[2]); err == nil {
							doc.PlaceOfIssue = strings.TrimSpace(m[1])
							doc.DateAwarded = t
							continue
						}
					}
					institution = append(institution, l)
					continue
				}
				// Everything after the award date is trailing information.
				if m := reLevel.FindStringSubmatch(l); m != nil {
					doc.NLQFLevel, doc.EQFLevel = m[1], m[2]
					continue
				}
				if strings.HasPrefix(l, labelDownload) {
					if doc.DownloadDate, err = parseDutchDate(strings.TrimSpace(strings.TrimPrefix(l, labelDownload))); err != nil {
						return nil, fmt.Errorf("%w: download date: %v", ErrNotADiploma, err)
					}
					continue
				}
				if m := reFooter.FindStringSubmatch(l); m != nil {
					doc.DocumentNumber = m[1]
					continue
				}
			}
			doc.Institution = strings.Join(institution, " ")
		}
	}

	if doc.DocumentType == "" || doc.Qualification == "" {
		return nil, fmt.Errorf("%w: qualification not found", ErrNotADiploma)
	}
	if doc.FullName == "" || doc.DateOfBirth.IsZero() {
		return nil, fmt.Errorf("%w: holder not found", ErrNotADiploma)
	}
	if doc.Institution == "" || doc.DateAwarded.IsZero() {
		return nil, fmt.Errorf("%w: issuing institution not found", ErrNotADiploma)
	}
	if doc.DocumentNumber == "" {
		return nil, fmt.Errorf("%w: document number not found", ErrNotADiploma)
	}

	for _, page := range pages.Rest {
		if strings.Contains(page, headingGradeList) {
			doc.HasGradeList = true
		}
	}
	return doc, nil
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

var dutchMonths = map[string]time.Month{
	"januari": time.January, "februari": time.February, "maart": time.March,
	"april": time.April, "mei": time.May, "juni": time.June, "juli": time.July,
	"augustus": time.August, "september": time.September, "oktober": time.October,
	"november": time.November, "december": time.December,
	"jan": time.January, "feb": time.February, "mrt": time.March, "apr": time.April,
	"jun": time.June, "jul": time.July, "aug": time.August, "sep": time.September,
	"okt": time.October, "nov": time.November, "dec": time.December,
}

// parseDutchDate parses dates as printed on the extract ("3 februari 1980"); the
// numeric forms "03-02-1980" and "1980-02-03" are accepted as well.
func parseDutchDate(s string) (time.Time, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	for _, layout := range []string{"02-01-2006", "2006-01-02", "2-1-2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	parts := strings.Fields(s)
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("unrecognised date %q", s)
	}
	var day, year int
	if _, err := fmt.Sscanf(parts[0], "%d", &day); err != nil {
		return time.Time{}, fmt.Errorf("unrecognised day in %q", s)
	}
	month, ok := dutchMonths[strings.ToLower(parts[1])]
	if !ok {
		return time.Time{}, fmt.Errorf("unrecognised month in %q", s)
	}
	if _, err := fmt.Sscanf(parts[2], "%d", &year); err != nil {
		return time.Time{}, fmt.Errorf("unrecognised year in %q", s)
	}
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if t.Day() != day {
		return time.Time{}, fmt.Errorf("invalid day in %q", s)
	}
	return t, nil
}
