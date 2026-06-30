package identity

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type Name struct {
	GivenNames string
	LastName   string
}

var particles = []string{
	"van", "von", "der", "den", "de", "ten", "ter", "te", "op", "aan",
	"del", "della", "di", "da", "dos", "das", "la", "le", "los", "las", "du", "y",
}

var (
	foldTransform = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	titleCaser    = cases.Title(language.Und)
	foldCaser     = cases.Fold()
)

func (n Name) Key() string {
	return fold(n.GivenNames) + "\x00" + fold(n.LastName)
}

func fold(s string) string {
	stripped, _, err := transform.String(foldTransform, s)
	if err != nil {
		stripped = s
	}
	return strings.Join(strings.Fields(foldCaser.String(stripped)), " ")
}

func (n Name) Clean() Name {
	return Name{GivenNames: cleanField(n.GivenNames), LastName: cleanField(n.LastName)}
}

func cleanField(s string) string {
	tokens := strings.Fields(s)
	if !isMRZShape(s) {
		return strings.Join(tokens, " ")
	}
	for i, tok := range tokens {
		lower := strings.ToLower(tok)
		if slices.Contains(particles, lower) {
			tokens[i] = lower
		} else {
			tokens[i] = titleCaser.String(lower)
		}
	}
	return strings.Join(tokens, " ")
}

func isMRZShape(s string) bool {
	hasUpper := false
	for _, r := range s {
		switch {
		case r > unicode.MaxASCII:
			return false
		case unicode.IsLower(r):
			return false
		case unicode.IsUpper(r):
			hasUpper = true
		}
	}
	return hasUpper
}

type Decision int

const (
	Populate Decision = iota
	Proceed
	Upgrade
	Review
)

func (d Decision) String() string {
	switch d {
	case Populate:
		return "populate"
	case Proceed:
		return "proceed"
	case Upgrade:
		return "upgrade"
	case Review:
		return "review"
	default:
		return "unknown"
	}
}

func Reconcile(disclosed Name, stored *Name) Decision {
	if stored == nil {
		return Populate
	}
	if disclosed.Key() != stored.Key() {
		return Review
	}
	if hasDiacritics(disclosed.Clean()) && !hasDiacritics(*stored) {
		return Upgrade
	}
	return Proceed
}

func hasDiacritics(n Name) bool {
	return hasNonASCIILetter(n.GivenNames) || hasNonASCIILetter(n.LastName)
}

func hasNonASCIILetter(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
