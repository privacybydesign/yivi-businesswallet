// Package pdftext holds what the Dutch PDF parsers (diploma, vog) share:
// grouping a page's positioned words into lines, and reading the dates they
// print.
package pdftext

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

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

// Line is a group of words that share (approximately) the same baseline.
type Line struct {
	Top   float64
	Words []Word
}

// Text is the line's words joined by single spaces.
func (l Line) Text() string {
	parts := make([]string, len(l.Words))
	for i, w := range l.Words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// lineTolerance is the maximum vertical distance (points) between two words on
// the same line. Both documents are rendered at ~10pt, so words on one line
// differ by a few points at most, while lines are ~10pt apart.
const lineTolerance = 4.0

// GroupLines groups words into lines, top of the page first, each line's
// words left to right.
func GroupLines(words []Word) []Line {
	sorted := make([]Word, len(words))
	copy(sorted, words)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Top != sorted[j].Top {
			return sorted[i].Top > sorted[j].Top
		}
		return sorted[i].Left < sorted[j].Left
	})

	var lines []Line
	for _, w := range sorted {
		if len(lines) > 0 {
			current := &lines[len(lines)-1]
			// Compare against the running maximum of the line so a slightly
			// lower word (descenders) still joins the line.
			if current.Top-w.Top <= lineTolerance {
				current.Words = append(current.Words, w)
				continue
			}
		}
		lines = append(lines, Line{Top: w.Top, Words: []Word{w}})
	}
	for i := range lines {
		sort.Slice(lines[i].Words, func(a, b int) bool {
			return lines[i].Words[a].Left < lines[i].Words[b].Left
		})
	}
	return lines
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

// numericDateLayouts are the numeric forms accepted next to the written one.
var numericDateLayouts = []string{"02-01-2006", "2006-01-02", "2-1-2006"}

// ParseDutchDate parses a date as Dutch documents print it ("25 maart 2026");
// the numeric forms "25-03-2026" and "2026-03-25" are accepted as well.
func ParseDutchDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	for _, layout := range numericDateLayouts {
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
