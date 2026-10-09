package diploma

import (
	"errors"
	"fmt"
	"slices"
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
// given names, which givenNamesMatch compares. When the surname is not found
// the first printed word is still compared with the first disclosed given name
// so the reasons stay informative.
func MatchFullName(fullName, dateOfBirth string, disclosed Person) Result {
	result := Result{}
	result.DateOfBirthMatch = compareDatesOfBirth(dateOfBirth, disclosed.DateOfBirth, &result.Reasons)

	tokens := nameTokens(fullName)
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

	result.GivenNamesMatch = givenNamesMatch(given, nameTokens(disclosed.GivenNames))
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
func surnameTails(p Person) [][]nameToken {
	var tails [][]nameToken
	seen := map[string]bool{}
	add := func(tokens []nameToken) {
		key := tokensKey(tokens)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		tails = append(tails, tokens)
	}
	for _, tokens := range surnameVariants(p) {
		add(tokens)
		for i := 1; i < len(tokens); i++ {
			rotated := append(append([]nameToken{}, tokens[i:]...), tokens[:i]...)
			add(rotated)
		}
	}
	return tails
}

func equalTokens(a, b []nameToken) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].equals(b[i]) {
			return false
		}
	}
	return true
}

// surnameVariants returns the normalised surname with and without prefix, and
// with the prefix trailing (as some MRZ transliterations do).
func surnameVariants(p Person) [][]nameToken {
	surname := nameTokens(p.Surname)
	prefix := nameTokens(p.Prefix)
	variants := [][]nameToken{surname}
	if len(prefix) > 0 {
		variants = append(variants,
			append(append([]nameToken{}, prefix...), surname...),
			append(append([]nameToken{}, surname...), prefix...))
	}
	return variants
}

// givenNamesMatch reports whether two lists of given names are the same
// person's: one lists the first given names of the other ("Anna" for "Anna
// Maria", as a passport may), or every printed given name is disclosed, in any
// order. A shared first name alone is not enough: "Anna Maria" is not "Anna
// Sophie".
func givenNamesMatch(documentTokens, disclosedTokens []nameToken) bool {
	if len(documentTokens) == 0 || len(disclosedTokens) == 0 {
		return false
	}
	shared := min(len(documentTokens), len(disclosedTokens))
	if equalTokens(documentTokens[:shared], disclosedTokens[:shared]) {
		return true
	}
	for _, document := range documentTokens {
		if !slices.ContainsFunc(disclosedTokens, document.equals) {
			return false
		}
	}
	return true
}

// SameName reports whether a and b are the same full name: as many words, each
// equal to its counterpart in order, plain or ICAO 9303 transliterated (see
// nameToken), case, accents and punctuation aside. A missing or extra name is
// a different name: "Anna Smit" is not "Anna Jansen Smit".
func SameName(a, b string) bool {
	tokens := nameTokens(a)
	return len(tokens) > 0 && equalTokens(tokens, nameTokens(b))
}

// nameToken is one word of a name in both spellings a document may use for
// it: plain (diacritics stripped, "Müller" → MULLER) and ICAO 9303
// transliterated ("Müller" → MUELLER, as a passport's MRZ prints it). Two
// words are the same when either spelling agrees, so a DUO extract printing
// "Müller" matches a passport reading MUELLER or MULLER, and nothing else.
type nameToken struct{ plain, icao string }

func (t nameToken) equals(other nameToken) bool {
	return t.plain == other.plain || t.icao == other.icao
}

// nameTokens splits a name into its words in both spellings. Both
// normalisations map a letter to letters and anything else to a space, so the
// two word lists always line up.
func nameTokens(s string) []nameToken {
	plain := strings.Fields(normalizeWith(s, plainTransliteration))
	icao := strings.Fields(normalizeWith(s, icaoTransliteration))
	tokens := make([]nameToken, len(plain))
	for i := range plain {
		tokens[i] = nameToken{plain: plain[i], icao: icao[i]}
	}
	return tokens
}

func tokensKey(tokens []nameToken) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = t.plain + "/" + t.icao
	}
	return strings.Join(parts, " ")
}

// plainTransliteration spells out the letters NFD does not decompose, so
// stripping diacritics alone would keep them as they are ("Søren" stays
// SØREN and never meets SOREN).
var plainTransliteration = map[rune]string{
	'ß': "SS", 'ẞ': "SS", 'Ø': "O", 'Æ': "AE", 'Œ': "OE", 'Þ': "TH", 'Ð': "D",
	'Ĳ': "IJ", 'Ł': "L", 'Đ': "D", 'Ħ': "H", 'Ŋ': "N", 'ı': "I",
}

// icaoTransliteration is the ICAO 9303 part 3 transliteration of the letters it
// spells out with two, on top of plainTransliteration.
var icaoTransliteration = func() map[rune]string {
	icao := map[rune]string{'Ä': "AE", 'Ö': "OE", 'Ü': "UE", 'Å': "AA", 'Ø': "OE"}
	for r, spelled := range plainTransliteration {
		if _, ok := icao[r]; !ok {
			icao[r] = spelled
		}
	}
	return icao
}()

// Normalize upper-cases, strips diacritics, turns punctuation into spaces and
// collapses whitespace so "Müller-Lüdenscheidt" and "MULLER LUDENSCHEIDT"
// compare equal. Letters without a decomposition are spelled out ("Strauß" →
// STRAUSS, "Søren" → SOREN).
func Normalize(s string) string {
	return normalizeWith(s, plainTransliteration)
}

func normalizeWith(s string, transliteration map[rune]string) string {
	var b strings.Builder
	for _, r := range norm.NFC.String(s) {
		upper := unicode.ToUpper(r)
		if spelled, ok := transliteration[upper]; ok {
			b.WriteString(spelled)
			continue
		}
		if spelled, ok := transliteration[r]; ok {
			b.WriteString(spelled)
			continue
		}
		for _, d := range norm.NFD.String(string(upper)) {
			switch {
			case unicode.Is(unicode.Mn, d):
				continue
			case unicode.IsLetter(d) || unicode.IsDigit(d):
				b.WriteRune(d)
			default:
				b.WriteRune(' ')
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// dateLayouts are the forms parseDutchDate does not read: the ones identity
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
	if t, err := parseDutchDate(s); err == nil {
		return t, nil
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}
