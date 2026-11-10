// Package euid parses European Unique Identifiers (BRIS, Directive 2017/1132)
// and maps them to ISO 6523 party identifiers.
//
// WE BUILD has not settled the EUID's string form. Parse reads the two forms it
// uses, the rulebook's `NLNHR.12345678` and EBWOID's `NLNHR.12345678.00001`;
// String writes one, chosen in canonicalForm alone.
package euid

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalid is returned for a string that is not an EUID in any accepted form.
var ErrInvalid = errors.New("invalid EUID")

// ErrNoICD is returned when a register has no known ISO 6523 ICD, or an ICD no
// known register.
var ErrNoICD = errors.New("no ISO 6523 mapping")

const (
	countryNL     = "NL"
	registerNHR   = "NHR"
	icdKVK        = "0106"
	partSep       = "."
	iso6523Prefix = "iso6523:"
	iso6523Sep    = ":"
)

var (
	// euidPattern is the rulebook form plus EBWOID's optional third part:
	// country, register, number[, sub-ID].
	euidPattern = regexp.MustCompile(`^([A-Z]{2})([A-Z]{3})\.([a-zA-Z0-9]+)(?:\.([a-zA-Z0-9]+))?$`)
	idPattern   = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	icdPattern  = regexp.MustCompile(`^[0-9]{4}$`)

	// countryRegisters pins a country to its BRIS register code.
	countryRegisters = map[string]string{
		countryNL: registerNHR,
	}

	numberPatterns = map[register]*regexp.Regexp{
		{countryNL, registerNHR}: regexp.MustCompile(`^[0-9]{8}$`),
	}

	// 0106 for KVK is taken from Peppol, WE BUILD does not name it yet.
	icds = map[register]string{
		{countryNL, registerNHR}: icdKVK,
	}
)

type register struct{ country, code string }

// EUID is a parsed identifier. Sub is EBWOID's optional third part, empty when
// absent.
type EUID struct {
	Country  string
	Register string
	Number   string
	Sub      string
}

// Parse accepts `NLNHR.12345678` (rulebook) and `NLNHR.12345678.00001`
// (EBWOID). A Dutch EUID must use the NHR register and an 8-digit KVK number.
func Parse(s string) (EUID, error) {
	s = strings.TrimSpace(s)
	m := euidPattern.FindStringSubmatch(s)
	if m == nil {
		return EUID{}, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	return validate(s, EUID{Country: m[1], Register: m[2], Number: m[3], Sub: m[4]})
}

// String writes the canonical form.
func (e EUID) String() string { return canonicalForm(e) }

// canonicalForm is the one place the written form is chosen.
func canonicalForm(e EUID) string {
	s := e.Country + e.Register + partSep + e.Number
	if e.Sub != "" {
		s += partSep + e.Sub
	}
	return s
}

func validate(input string, e EUID) (EUID, error) {
	if want, ok := countryRegisters[e.Country]; ok && e.Register != want {
		return EUID{}, fmt.Errorf("%w: %q: %s EUIDs use register %s", ErrInvalid, input, e.Country, want)
	}
	if p, ok := numberPatterns[register{e.Country, e.Register}]; ok && !p.MatchString(e.Number) {
		return EUID{}, fmt.Errorf("%w: %q: bad registration number for %s%s", ErrInvalid, input, e.Country, e.Register)
	}
	return e, nil
}

// ISO6523 is an ISO 6523 party identifier: an ICD and the id within it.
type ISO6523 struct {
	ICD string
	ID  string
}

// String writes `<ICD>:<id>`, e.g. `0106:12345678`.
func (p ISO6523) String() string { return p.ICD + iso6523Sep + p.ID }

// ISO6523 maps e to its ISO 6523 party identifier. The id is the company's
// number: ISO 6523 has no place for the sub-ID, so it is dropped.
func (e EUID) ISO6523() (ISO6523, error) {
	icd, ok := icds[register{e.Country, e.Register}]
	if !ok {
		return ISO6523{}, fmt.Errorf("%w for %s", ErrNoICD, e)
	}
	return ISO6523{ICD: icd, ID: e.Number}, nil
}

// ParseISO6523 accepts `0106:12345678`, optionally prefixed up to `iso6523:`
// (e.g. `ebcore:iso6523:0106:12345678`).
func ParseISO6523(s string) (ISO6523, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, iso6523Prefix); i >= 0 {
		s = s[i+len(iso6523Prefix):]
	}
	icd, id, ok := strings.Cut(s, iso6523Sep)
	if !ok || !icdPattern.MatchString(icd) || !idPattern.MatchString(id) {
		return ISO6523{}, fmt.Errorf("%w: ISO 6523 %q", ErrInvalid, s)
	}
	return ISO6523{ICD: icd, ID: id}, nil
}

// EUID maps p back to the EUID of its register.
func (p ISO6523) EUID() (EUID, error) {
	for reg, icd := range icds {
		if icd == p.ICD {
			return validate(p.String(), EUID{Country: reg.country, Register: reg.code, Number: p.ID})
		}
	}
	return EUID{}, fmt.Errorf("%w for ICD %s", ErrNoICD, p.ICD)
}
