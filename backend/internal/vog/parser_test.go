package vog

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog/vogtest"
)

func buildTestPDF(t *testing.T, lines []string) []byte {
	t.Helper()
	return vogtest.BuildTestPDF(t, lines)
}

func validVOGLines() []string {
	return []string{
		"kenmerk: ABC12345XYZ",
		"Datum: 01-02-2024",
		"Geslachtsnaam: Jansen",
		"Tussenvoegsels: van der",
		"Voornamen: Jan Willem",
		"Geboortedatum: 03-04-1990",
		"profiel: 11 43 91",
	}
}

func TestParseValidDocument(t *testing.T) {
	doc, err := Parse(buildTestPDF(t, validVOGLines()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Reference != "ABC12345XYZ" {
		t.Errorf("Reference = %q, want ABC12345XYZ", doc.Reference)
	}
	if doc.GivenNames != "Jan Willem" {
		t.Errorf("GivenNames = %q, want %q", doc.GivenNames, "Jan Willem")
	}
	if doc.Surname != "van der Jansen" {
		t.Errorf("Surname = %q, want %q", doc.Surname, "van der Jansen")
	}
	if doc.DateOfBirth != "1990-04-03" {
		t.Errorf("DateOfBirth = %q, want 1990-04-03", doc.DateOfBirth)
	}
	if got, want := doc.IssueDate.Format("2006-01-02"), "2024-02-01"; got != want {
		t.Errorf("IssueDate = %q, want %q", got, want)
	}
	if got, want := doc.AspectCodes, []string{"11", "43", "91"}; !equalStrings(got, want) {
		t.Errorf("AspectCodes = %v, want %v", got, want)
	}
	if len(doc.ProfileCodes) != 0 {
		t.Errorf("ProfileCodes = %v, want none", doc.ProfileCodes)
	}
}

// TestParseLabelValueOnNextLine covers the other common form layout: a
// label-only line followed by the value on its own line.
func TestParseLabelValueOnNextLine(t *testing.T) {
	lines := []string{
		"kenmerk",
		"XYZ999",
		"Voornamen",
		"Alex",
		"Geslachtsnaam: Bakker",
		"Geboortedatum: 15-06-1985",
		"Datum: 20-01-2023",
	}
	doc, err := Parse(buildTestPDF(t, lines))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Reference != "XYZ999" {
		t.Errorf("Reference = %q, want XYZ999", doc.Reference)
	}
	if doc.GivenNames != "Alex" {
		t.Errorf("GivenNames = %q, want Alex", doc.GivenNames)
	}
}

func TestParseNotAVOG(t *testing.T) {
	_, err := Parse(buildTestPDF(t, []string{"This is just some other document"}))
	if err == nil {
		t.Fatal("Parse: want error for a document with no kenmerk")
	}
}

func TestParseMissingDateOfBirth(t *testing.T) {
	lines := []string{
		"kenmerk: ABC1",
		"Datum: 01-02-2024",
		"Voornamen: Jan",
		"Geslachtsnaam: Jansen",
	}
	_, err := Parse(buildTestPDF(t, lines))
	if err == nil {
		t.Fatal("Parse: want error when geboortedatum is missing")
	}
}

func TestProfileCodesContinuationLine(t *testing.T) {
	lines := []string{
		"kenmerk: ABC1",
		"Datum: 01-02-2024",
		"Voornamen: Jan",
		"Geslachtsnaam: Jansen",
		"Geboortedatum: 03-04-1990",
		"profiel: 11 12",
		"13 21",
	}
	doc, err := Parse(buildTestPDF(t, lines))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"11", "12", "13", "21"}
	if !equalStrings(doc.AspectCodes, want) {
		t.Errorf("AspectCodes = %v, want %v", doc.AspectCodes, want)
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
