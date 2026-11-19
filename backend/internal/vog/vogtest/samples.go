// Package vogtest holds the two real Justis VOG documents the parser was
// verified against (issued to a project member, who contributed them as test
// fixtures; go-vog-issuer's test suite uses the same two), with the values
// printed on them, so internal/vog's tests and internal/integration's HTTP
// flows can exercise a genuine encrypted VOG rather than a synthetic PDF.
// This package must not import internal/vog: vog's own tests import it.
package vogtest

import (
	_ "embed"
	"time"
)

//go:embed testdata/vog-9999012026032500922.pdf
var sample1 []byte

//go:embed testdata/vog-9999012026050801510.pdf
var sample2 []byte

// Sample is one real VOG PDF with the fields printed on it.
type Sample struct {
	PDF         []byte
	Reference   string
	IssueDate   time.Time
	GivenNames  string
	Surname     string
	DateOfBirth time.Time
	// Codes are the profile codes on the document, in print order; both
	// samples carry function aspects only.
	Codes []string
}

// Samples returns the two documents. Callers must not modify the returned PDF
// bytes.
func Samples() []Sample {
	return []Sample{
		{
			PDF:         sample1,
			Reference:   "9999012026032500922",
			IssueDate:   time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
			GivenNames:  "Dibran",
			Surname:     "Mulder",
			DateOfBirth: time.Date(1991, 5, 14, 0, 0, 0, 0, time.UTC),
			Codes:       []string{"84", "85"},
		},
		{
			PDF:         sample2,
			Reference:   "9999012026050801510",
			IssueDate:   time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
			GivenNames:  "Dibran",
			Surname:     "Mulder",
			DateOfBirth: time.Date(1991, 5, 14, 0, 0, 0, 0, time.UTC),
			Codes:       []string{"11", "12", "84", "85"},
		},
	}
}
