package vog

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog/vogtest"
)

// sharedParser is initialised once: compiling the PDFium WebAssembly module
// takes seconds.
var sharedParser *PDFiumParser

func TestMain(m *testing.M) {
	parser, err := NewPDFiumParser()
	if err != nil {
		panic(err)
	}
	sharedParser = parser
	code := m.Run()
	_ = parser.Close()
	os.Exit(code)
}

// TestParseRealVOG runs the parser against the two genuine, AES-encrypted
// Justis documents - the check the previous digitorus/pdf parser never had,
// and which would have caught it rejecting every real VOG.
func TestParseRealVOG(t *testing.T) {
	for _, sample := range vogtest.Samples() {
		t.Run(sample.Reference, func(t *testing.T) {
			doc, err := sharedParser.Parse(sample.PDF)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if doc.Reference != sample.Reference {
				t.Errorf("Reference = %q, want %q", doc.Reference, sample.Reference)
			}
			if !doc.IssueDate.Equal(sample.IssueDate) {
				t.Errorf("IssueDate = %v, want %v", doc.IssueDate, sample.IssueDate)
			}
			if doc.GivenNames != sample.GivenNames {
				t.Errorf("GivenNames = %q, want %q", doc.GivenNames, sample.GivenNames)
			}
			if doc.Surname != sample.Surname {
				t.Errorf("Surname = %q, want %q", doc.Surname, sample.Surname)
			}
			if want := sample.DateOfBirth.Format("2006-01-02"); doc.DateOfBirth != want {
				t.Errorf("DateOfBirth = %q, want %q", doc.DateOfBirth, want)
			}
			if !equalStrings(doc.AspectCodes, sample.Codes) {
				t.Errorf("AspectCodes = %v, want %v", doc.AspectCodes, sample.Codes)
			}
			if len(doc.ProfileCodes) != 0 {
				t.Errorf("ProfileCodes = %v, want none", doc.ProfileCodes)
			}
		})
	}
}

func TestParseRejectsNonPDF(t *testing.T) {
	_, err := sharedParser.Parse([]byte("this is not a pdf"))
	if !errors.Is(err, ErrNotAVOG) {
		t.Fatalf("err = %v, want ErrNotAVOG", err)
	}
}

// word builds a Word at the given position with a nominal 10pt height.
func word(text string, left, top float64) Word {
	return Word{Text: text, Left: left, Top: top, Right: left + float64(len(text))*5, Bottom: top - 8}
}

// syntheticVOG mimics the layout of a real VOG: English label, Dutch label and
// value on one line, value column starting at x=227.
func syntheticVOG(prefix string) []Word {
	words := []Word{
		word("Verklaring", 67, 771), word("Omtrent", 139, 771), word("het", 197, 771), word("Gedrag", 222, 771),
		word("Date", 125, 600), word("Datum", 150, 600), word("1", 228, 600), word("oktober", 236, 600), word("2025", 270, 600),
		word("Our", 86, 590), word("reference", 105, 590), word("Ons", 149, 590), word("kenmerk", 169, 590), word("1234567890123456789", 227, 590),
		word("Surname", 106, 580), word("Geslachtsnaam", 149, 580), word("Berg", 228, 580),
		word("Prefix", 69, 571), word("to", 97, 570), word("surname", 108, 569), word("Tussenvoegsels", 149, 571),
		word("Given", 90, 560), word("names", 117, 559), word("Voorna(a)m(en)", 149, 561), word("Anna", 228, 561), word("Maria", 250, 561),
		word("Date", 91, 550), word("of", 114, 551), word("birth", 125, 551), word("Geboortedatum", 149, 551), word("3", 228, 550), word("februari", 236, 551), word("1980", 275, 551),
		word("Place", 89, 541), word("of", 114, 541), word("birth", 125, 541), word("Geboorteplaats", 149, 541), word("Den", 228, 541), word("Haag", 248, 541),
		word("Country", 77, 531), word("of", 114, 531), word("birth", 125, 531), word("Geboorteland", 149, 531), word("Nederland", 228, 531),
		word("Hierbij", 150, 507), word("geef", 178, 507), word("ik", 199, 507), word("u", 209, 505), word("de", 216, 507), word("VOG", 228, 507), word("die", 250, 507), word("u", 265, 505), word("nodig", 272, 507), word("heeft", 297, 507), word("voor:", 320, 505),
		word("Medewerker", 149, 495), word("kinderopvang", 200, 495),
		word("Uit", 150, 471), word("de", 163, 471), word("screening", 176, 471),
		word("Er", 150, 435), word("is", 161, 435), word("bij", 170, 435), word("deze", 182, 435), word("screening", 204, 435), word("uitgegaan", 246, 435), word("van", 288, 433), word("het", 306, 435), word("volgende", 321, 435), word("profiel:", 361, 435),
		word("21,", 149, 423), word("84,", 166, 423), word("86,", 181, 423), word("55", 196, 423),
		word("Op", 149, 399), word("de", 163, 399), word("volgende", 176, 399), word("pagina", 215, 399),
	}
	if prefix != "" {
		words = append(words, word(prefix, 228, 571))
	}
	return words
}

func TestExtractDocumentSynthetic(t *testing.T) {
	doc, err := ExtractDocument(syntheticVOG("van den"))
	if err != nil {
		t.Fatalf("ExtractDocument: %v", err)
	}
	if doc.Reference != "1234567890123456789" {
		t.Errorf("Reference = %q", doc.Reference)
	}
	if want := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC); !doc.IssueDate.Equal(want) {
		t.Errorf("IssueDate = %v, want %v", doc.IssueDate, want)
	}
	if doc.Surname != "van den Berg" {
		t.Errorf("Surname = %q, want %q", doc.Surname, "van den Berg")
	}
	if doc.GivenNames != "Anna Maria" {
		t.Errorf("GivenNames = %q, want %q", doc.GivenNames, "Anna Maria")
	}
	if doc.DateOfBirth != "1980-02-03" {
		t.Errorf("DateOfBirth = %q, want 1980-02-03", doc.DateOfBirth)
	}
	// 21, 84, 86 are function aspects; 55 is a specific-profile number.
	if want := []string{"21", "84", "86"}; !equalStrings(doc.AspectCodes, want) {
		t.Errorf("AspectCodes = %v, want %v", doc.AspectCodes, want)
	}
	if want := []string{"55"}; !equalStrings(doc.ProfileCodes, want) {
		t.Errorf("ProfileCodes = %v, want %v", doc.ProfileCodes, want)
	}
}

func TestExtractDocumentWithoutPrefix(t *testing.T) {
	doc, err := ExtractDocument(syntheticVOG(""))
	if err != nil {
		t.Fatalf("ExtractDocument: %v", err)
	}
	if doc.Surname != "Berg" {
		t.Errorf("Surname = %q, want Berg", doc.Surname)
	}
}

func TestExtractDocumentNotAVOG(t *testing.T) {
	for name, words := range map[string][]Word{
		"no words":   nil,
		"a letter":   {word("Some", 10, 700), word("letter", 40, 700)},
		"no title":   without(syntheticVOG(""), "Verklaring"),
		"title only": {word("Verklaring", 67, 771), word("Omtrent", 139, 771), word("het", 197, 771), word("Gedrag", 222, 771)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ExtractDocument(words)
			if name == "title only" {
				// A title with nothing under it is a VOG whose fields cannot be read.
				if !errors.Is(err, ErrUnparseable) {
					t.Fatalf("err = %v, want ErrUnparseable", err)
				}
				return
			}
			if !errors.Is(err, ErrNotAVOG) {
				t.Fatalf("err = %v, want ErrNotAVOG", err)
			}
		})
	}
}

func TestExtractDocumentUnparseable(t *testing.T) {
	for name, words := range map[string][]Word{
		"no date of birth": without(syntheticVOG(""), "Geboortedatum"),
		"no given names":   without(syntheticVOG(""), "Voorna(a)m(en)"),
		"no profile codes": without(syntheticVOG(""), "21,", "84,", "86,", "55"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ExtractDocument(words)
			if !errors.Is(err, ErrUnparseable) {
				t.Fatalf("err = %v, want ErrUnparseable", err)
			}
		})
	}
}

// without drops every word whose text is one of drop.
func without(words []Word, drop ...string) []Word {
	var out []Word
	for _, w := range words {
		skip := false
		for _, d := range drop {
			if w.Text == d {
				skip = true
			}
		}
		if !skip {
			out = append(out, w)
		}
	}
	return out
}

func TestParseDutchDate(t *testing.T) {
	valid := map[string]time.Time{
		"25 maart 2026":   time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
		"8 mei 2026":      time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
		"14 Mei 1991":     time.Date(1991, 5, 14, 0, 0, 0, 0, time.UTC),
		"01-02-2003":      time.Date(2003, 2, 1, 0, 0, 0, 0, time.UTC),
		"2003-02-01":      time.Date(2003, 2, 1, 0, 0, 0, 0, time.UTC),
		"1 augustus 1999": time.Date(1999, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	for input, want := range valid {
		got, err := ParseDutchDate(input)
		if err != nil {
			t.Errorf("ParseDutchDate(%q): %v", input, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("ParseDutchDate(%q) = %v, want %v", input, got, want)
		}
	}
	for _, invalid := range []string{"", "maart 2026", "31 februari 2026", "25 march 2026", "foo"} {
		if _, err := ParseDutchDate(invalid); err == nil {
			t.Errorf("ParseDutchDate(%q): want error", invalid)
		}
	}
}

func equalStrings(a, b []string) bool {
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
