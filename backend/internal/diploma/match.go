package diploma

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Person is the identity to compare, as printed on a document or as disclosed.
type Person struct {
	// Given names / first names, space separated.
	GivenNames string
	// Surname without prefix (geslachtsnaam). For sources that do not split
	// prefix and surname (passport MRZ, driving licence) put the full last
	// name here and leave Prefix empty.
	Surname string
	// Surname prefix (tussenvoegsel), e.g. "van der". Optional.
	Prefix string
	// Date of birth in any of the formats accepted by ParseDate.
	DateOfBirth string
}

// Result explains the outcome of a comparison.
type Result struct {
	Matched          bool     `json:"matched"`
	DateOfBirthMatch bool     `json:"date_of_birth_match"`
	SurnameMatch     bool     `json:"surname_match"`
	GivenNamesMatch  bool     `json:"given_names_match"`
	Reasons          []string `json:"reasons,omitempty"`
}

// MatchFullName compares a holder whose name is printed as a single line, the
// way DUO prints it on a diploma extract ("Anna Maria van der Berg"), with the
// disclosed person. The disclosed surname (with or without its prefix, in
// either order) must be the tail of the printed name; what precedes it are the
// given names, which are compared as in Match. When the surname is not found
// the first printed word is still compared with the first disclosed given name
// so the reasons stay informative.
func MatchFullName(fullName, dateOfBirth string, disclosed Person) Result {
	result := Result{}
	result.DateOfBirthMatch = compareDatesOfBirth(dateOfBirth, disclosed.DateOfBirth, &result.Reasons)

	tokens := strings.Fields(Normalize(fullName))
	given := tokens
	for _, surname := range surnameTails(disclosed) {
		if len(surname) == 0 || len(surname) >= len(tokens) {
			continue
		}
		if equalTokens(tokens[len(tokens)-len(surname):], surname) {
			result.SurnameMatch = true
			given = tokens[:len(tokens)-len(surname)]
			break
		}
	}
	if !result.SurnameMatch {
		result.Reasons = append(result.Reasons, "surname differs")
		if len(tokens) > 0 {
			given = tokens[:1]
		}
	}

	result.GivenNamesMatch = givenNamesMatch(strings.Join(given, " "), disclosed.GivenNames)
	if !result.GivenNamesMatch {
		result.Reasons = append(result.Reasons, "given names differ")
	}

	result.Matched = result.DateOfBirthMatch && result.SurnameMatch && result.GivenNamesMatch
	return result
}

// compareDatesOfBirth parses both dates and reports whether they are equal,
// appending a reason when they are unreadable or differ.
func compareDatesOfBirth(document, disclosed string, reasons *[]string) bool {
	documentDob, err := ParseDate(document)
	if err != nil {
		*reasons = append(*reasons, fmt.Sprintf("document date of birth unreadable: %v", err))
	}
	disclosedDob, err := ParseDate(disclosed)
	if err != nil {
		*reasons = append(*reasons, fmt.Sprintf("disclosed date of birth unreadable: %v", err))
	}
	if documentDob.IsZero() || disclosedDob.IsZero() {
		return false
	}
	if !documentDob.Equal(disclosedDob) {
		*reasons = append(*reasons, "date of birth differs")
		return false
	}
	return true
}

// surnameTails returns the token sequences that may end a printed full name
// for the disclosed person: the surname variants of surnameVariants plus every
// rotation of a multi-word surname, because travel documents may print "Berg
// van der" for a name DUO prints as "van der Berg".
func surnameTails(p Person) [][]string {
	var tails [][]string
	seen := map[string]bool{}
	add := func(tokens []string) {
		key := strings.Join(tokens, " ")
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		tails = append(tails, tokens)
	}
	for _, variant := range surnameVariants(p) {
		tokens := strings.Fields(variant)
		add(tokens)
		for i := 1; i < len(tokens); i++ {
			rotated := append(append([]string{}, tokens[i:]...), tokens[:i]...)
			add(rotated)
		}
	}
	return tails
}

func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// surnameVariants returns the normalised surname with and without prefix, and
// with the prefix trailing (as some MRZ transliterations do).
func surnameVariants(p Person) []string {
	surname := Normalize(p.Surname)
	prefix := Normalize(p.Prefix)
	variants := []string{surname}
	if prefix != "" {
		variants = append(variants, prefix+" "+surname, surname+" "+prefix)
	}
	return variants
}

func givenNamesMatch(documentNames, disclosedNames string) bool {
	documentTokens := strings.Fields(Normalize(documentNames))
	disclosedTokens := strings.Fields(Normalize(disclosedNames))
	if len(documentTokens) == 0 || len(disclosedTokens) == 0 {
		return false
	}
	if documentTokens[0] == disclosedTokens[0] {
		return true
	}
	disclosedSet := map[string]bool{}
	for _, t := range disclosedTokens {
		disclosedSet[t] = true
	}
	for _, t := range documentTokens {
		if !disclosedSet[t] {
			return false
		}
	}
	return true
}

// Normalize upper-cases, strips diacritics, turns punctuation into spaces and
// collapses whitespace so "Müller-Lüdenscheidt" and "MULLER LUDENSCHEIDT"
// compare equal.
func Normalize(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// dateLayouts are the forms ParseDutchDate does not read: the ones identity
// sources other than DUO write.
var dateLayouts = []string{
	"02/01/2006",
	"20060102",
	"2 January 2006",
	"02 January 2006",
	time.RFC3339,
}

// ParseDate accepts the date formats used by DUO ("3 februari 1980"), the BRP
// credential ("03-02-1980") and the travel document credentials
// ("1980-02-03") and returns the calendar date in UTC.
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	if t, err := ParseDutchDate(s); err == nil {
		return t, nil
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}
