package employment

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// ErrNotAStatement is a PDF that does not read as a Verzekeringsbericht.
var ErrNotAStatement = errors.New("document is not a UWV Verzekeringsbericht")

// Parser reads a Statement from a PDF.
type Parser interface {
	Parse(ctx context.Context, pdf []byte) (*Statement, error)
}

// TextReader returns the plain text of every page of a PDF (implemented by
// *diploma.PDFiumParser, on its sandboxed pool and limits).
type TextReader interface {
	Text(ctx context.Context, pdf []byte) ([]string, error)
}

// TextParser parses the text a TextReader reads.
type TextParser struct {
	text TextReader
}

func NewTextParser(text TextReader) *TextParser {
	return &TextParser{text: text}
}

// Parse reads the statement in pdf.
func (p *TextParser) Parse(ctx context.Context, pdf []byte) (*Statement, error) {
	pages, err := p.text.Text(ctx, pdf)
	if errors.Is(err, diploma.ErrUnreadable) {
		return nil, fmt.Errorf("%w: %w", ErrNotAStatement, err)
	}
	if err != nil {
		return nil, err
	}
	return parseStatement(pages)
}

// What every page of a statement prints, apart from its own content.
const (
	footerTitle       = "Printversie verzekeringsbericht"
	labelEmployer     = "Werkgever/Instantie"
	labelPayrollTax   = "Loonheffingennummer"
	labelContractForm = "Contractvorm"
	headingPeriods    = "Periode Aantal uur Sv-loon"
	headingDisclaimer = "Disclaimer:"
	// dateLayout is how the statement prints a date of birth and a period.
	dateLayout = "02-01-2006"
)

var (
	// reFormCode is UWV's form code, which ends a page's last line or stands
	// on its own ("VZB-006").
	reFormCode = regexp.MustCompile(`\s*\bVZB-\d+$`)
	// reHolderDates is the footer line under the holder's name.
	reHolderDates = regexp.MustCompile(`^Geboortedatum:\s*(\d{2}-\d{2}-\d{4})\s+Datum:\s*(\d{1,2} \S+ \d{4})$`)
	rePageNumber  = regexp.MustCompile(`^Pagina \d+ van \d+$`)
	// rePeriod starts a month line: "01-09-2026 t/m 30-09-2026 173 € 3.163,56",
	// its hours after the period.
	rePeriod = regexp.MustCompile(`^(\d{2}-\d{2}-\d{4}) t/m (\d{2}-\d{2}-\d{4})(?:\s+(\d+)\b)?`)
	// reInitials is a word of initials: "J.", "J.Th." or "IJ.".
	reInitials = regexp.MustCompile(`^(?:(?:IJ|\p{Lu}\p{Ll}*)\.)+$`)
	reInitial  = regexp.MustCompile(`(IJ|\p{Lu}\p{Ll}*)\.`)
)

// footer is the holder lines every page ends with.
type footer struct {
	holder, dates string
}

// parseStatement interprets the pages' text: a footer on every page naming
// the holder, and in the "Loongegevens" part one block per relation, each a
// few labelled lines followed by one line per month.
func parseStatement(pages []string) (*Statement, error) {
	var lines []string
	var foot *footer
	formCode := false
	for _, page := range pages {
		pageLines := splitLines(page)
		if stripFormCode(pageLines) {
			formCode = true
		}

		pageFoot, err := pageFooter(pageLines)
		if err != nil {
			return nil, err
		}
		if foot != nil && *foot != pageFoot {
			return nil, fmt.Errorf("%w: pages name different holders", ErrNotAStatement)
		}

		foot = &pageFoot
		lines = append(lines, bodyLines(pageLines, pageFoot)...)
	}
	if foot == nil || !formCode {
		return nil, fmt.Errorf("%w: no UWV form code or holder", ErrNotAStatement)
	}

	st := &Statement{}
	if err := parseHolder(st, *foot); err != nil {
		return nil, err
	}
	relations, err := parseRelations(lines)
	if err != nil {
		return nil, err
	}
	st.Relations = relations
	return st, nil
}

// stripFormCode removes the form code from the lines that print it, and
// reports whether any did.
func stripFormCode(lines []string) bool {
	found := false
	for i, l := range lines {
		if reFormCode.MatchString(l) {
			found = true
			lines[i] = strings.TrimSpace(reFormCode.ReplaceAllString(l, ""))
		}
	}
	return found
}

// bodyLines drops the page's footer, title and page number.
func bodyLines(lines []string, foot footer) []string {
	var body []string
	for _, l := range lines {
		if l != "" && l != foot.holder && l != foot.dates && l != footerTitle && !rePageNumber.MatchString(l) {
			body = append(body, l)
		}
	}
	return body
}

func splitLines(page string) []string {
	raw := strings.FieldsFunc(page, func(r rune) bool { return r == '\n' || r == '\r' })
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	return lines
}

// pageFooter finds the page's "Geboortedatum: … Datum: …" line and the
// holder line above it; a page without them is no page of a statement.
func pageFooter(lines []string) (footer, error) {
	for i, l := range lines {
		if !reHolderDates.MatchString(l) || i == 0 || lines[i-1] == "" {
			continue
		}
		if !slices.Contains(lines, footerTitle) {
			break
		}
		return footer{holder: lines[i-1], dates: l}, nil
	}
	return footer{}, fmt.Errorf("%w: a page without the holder footer", ErrNotAStatement)
}

// parseHolder reads "De heer J.Th. de Vries" (the salutation, the initials,
// then the surname) and the dates under it.
func parseHolder(st *Statement, foot footer) error {
	words := strings.Fields(foot.holder)
	first := -1
	for i, w := range words {
		if reInitials.MatchString(w) {
			first = i
			break
		}
	}
	if first < 0 {
		return fmt.Errorf("%w: holder %q prints no initials", ErrNotAStatement, foot.holder)
	}
	end := first
	for end < len(words) && reInitials.MatchString(words[end]) {
		for _, m := range reInitial.FindAllStringSubmatch(words[end], -1) {
			st.Initials = append(st.Initials, m[1])
		}
		end++
	}
	st.Surname = strings.Join(words[end:], " ")
	if st.Surname == "" {
		return fmt.Errorf("%w: holder %q prints no surname", ErrNotAStatement, foot.holder)
	}

	m := reHolderDates.FindStringSubmatch(foot.dates)
	dob, err := time.Parse(dateLayout, m[1])
	if err != nil {
		return fmt.Errorf("%w: date of birth: %w", ErrNotAStatement, err)
	}
	date, err := diploma.ParseDate(m[2])
	if err != nil {
		return fmt.Errorf("%w: statement date: %w", ErrNotAStatement, err)
	}
	st.DateOfBirth, st.Date = dob, date
	return nil
}

// parseRelations reads the relation blocks. A block continued on the next
// page repeats its labels, so blocks with the same employer, number and
// contract form are one relation over all their months.
func parseRelations(lines []string) ([]Relation, error) {
	var relations []Relation
	var current *Relation
	inContract := false
	for _, l := range lines {
		if strings.HasPrefix(l, headingDisclaimer) {
			break
		}
		switch {
		case strings.HasPrefix(l, labelEmployer):
			relations = append(relations, Relation{Employer: strings.TrimSpace(strings.TrimPrefix(l, labelEmployer))})
			current, inContract = &relations[len(relations)-1], false
		case current == nil:
			// The "Arbeidsverleden" part before the first relation.
		case strings.HasPrefix(l, labelPayrollTax):
			current.PayrollTaxNumber = strings.TrimSpace(strings.TrimPrefix(l, labelPayrollTax))
		case strings.HasPrefix(l, labelContractForm):
			current.ContractForm, inContract = strings.TrimSpace(strings.TrimPrefix(l, labelContractForm)), true
		case l == headingPeriods:
			inContract = false
		case inContract && !rePeriod.MatchString(l):
			current.ContractForm = strings.TrimSpace(current.ContractForm + " " + l)
		default:
			if err := addPeriod(current, l); err != nil {
				return nil, err
			}
		}
	}
	return mergeRelations(relations), nil
}

// addPeriod widens r to the month on line l, when it is a month line.
func addPeriod(r *Relation, l string) error {
	m := rePeriod.FindStringSubmatch(l)
	if m == nil {
		return nil
	}
	from, err := time.Parse(dateLayout, m[1])
	if err != nil {
		return fmt.Errorf("%w: period %q: %w", ErrNotAStatement, l, err)
	}
	to, err := time.Parse(dateLayout, m[2])
	if err != nil {
		return fmt.Errorf("%w: period %q: %w", ErrNotAStatement, l, err)
	}
	if r.From.IsZero() || from.Before(r.From) {
		r.From = from
	}
	if to.After(r.To) {
		r.To = to
	}
	if m[3] != "" && m[3] != "0" && to.After(r.Worked) {
		r.Worked = to
	}
	return nil
}

// mergeRelations joins the blocks of one relation, drops a block that
// reports no month, and classifies each.
func mergeRelations(blocks []Relation) []Relation {
	type key struct{ employer, number, contract string }
	merged := []Relation{}
	at := map[key]int{}
	for _, b := range blocks {
		if b.From.IsZero() {
			continue
		}
		k := key{b.Employer, b.PayrollTaxNumber, b.ContractForm}
		i, seen := at[k]
		if !seen {
			at[k] = len(merged)
			merged = append(merged, b)
			continue
		}
		if b.From.Before(merged[i].From) {
			merged[i].From = b.From
		}
		if b.To.After(merged[i].To) {
			merged[i].To = b.To
		}
		if b.Worked.After(merged[i].Worked) {
			merged[i].Worked = b.Worked
		}
	}

	for i := range merged {
		merged[i].Kind = classify(merged[i].Employer, merged[i].PayrollTaxNumber)
	}
	return merged
}
