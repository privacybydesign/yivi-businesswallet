package diploma

import (
	"errors"
	"testing"
	"time"
)

// word builds a Word at the given position with a nominal 10pt height.
func word(text string, top float64) Word {
	return Word{Text: text, Left: 70, Top: top, Right: 70 + float64(len(text))*5, Bottom: top - 8}
}

// syntheticExtract mimics the layout of a real extract, one line per word.
func syntheticExtract(lines ...string) *Pages {
	pages := &Pages{}
	top := 800.0
	for _, l := range lines {
		pages.First = append(pages.First, word(l, top))
		top -= 12
	}
	return pages
}

func TestExtractDocumentSynthetic(t *testing.T) {
	doc, err := ExtractDocument(syntheticExtract(
		"Uit het register onderwijsdeelnemers",
		"DIPLOMA",
		"Middelbaar beroepsonderwijs",
		"Verpleegkundige (niveau 4)",
		"BEHAALD DOOR",
		"Anna Maria van der Berg",
		"Geboren op 3 februari 1980",
		"UITGEGEVEN DOOR",
		"ROC Midden Nederland",
		"Utrecht, 1 juli 2001",
		"Dit diploma heeft niveau NLQF 4 / EQF 4.",
		"Downloaddatum: 2 september 2026",
		"Dit digitale document is een officieel bewijs van diplomagegevens. 123456 - pagina 1/1",
		"Het vervalsen van dit document is strafbaar. DUO doet dan aangifte.",
	))
	if err != nil {
		t.Fatalf("ExtractDocument: %v", err)
	}
	checks := []struct {
		field     string
		got, want any
	}{
		{"DocumentType", doc.DocumentType, "Diploma"},
		{"Qualification", doc.Qualification, "Middelbaar beroepsonderwijs Verpleegkundige (niveau 4)"},
		{"Profiles", len(doc.Profiles), 0},
		{"FullName", doc.FullName, "Anna Maria van der Berg"},
		{"DateOfBirth", doc.DateOfBirth, time.Date(1980, 2, 3, 0, 0, 0, 0, time.UTC)},
		{"Institution", doc.Institution, "ROC Midden Nederland"},
		{"PlaceOfIssue", doc.PlaceOfIssue, "Utrecht"},
		{"DateAwarded", doc.DateAwarded, time.Date(2001, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"NLQFLevel", doc.NLQFLevel, "4"},
		{"EQFLevel", doc.EQFLevel, "4"},
		{"DownloadDate", doc.DownloadDate, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
		{"DocumentNumber", doc.DocumentNumber, "123456"},
		{"Pages", doc.Pages, 1},
		{"HasGradeList", doc.HasGradeList, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

func TestExtractDocumentErrors(t *testing.T) {
	cases := map[string]*Pages{
		"empty":     {},
		"a VOG":     syntheticExtract("Verklaring Omtrent het Gedrag", "Datum 1 oktober 2025"),
		"no holder": syntheticExtract("Uit het register onderwijsdeelnemers", "DIPLOMA", "Iets", "UITGEGEVEN DOOR", "School", "Ergens, 1 juli 2001", "1 - pagina 1/1"),
	}
	for name, pages := range cases {
		if _, err := ExtractDocument(pages); !errors.Is(err, ErrNotADiploma) {
			t.Errorf("%s: ExtractDocument error = %v, want ErrNotADiploma", name, err)
		}
	}
}

func TestParseDutchDate(t *testing.T) {
	want := time.Date(1980, 2, 3, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"3 februari 1980", "03-02-1980", "1980-02-03", "3 februari 1980."} {
		got, err := ParseDutchDate(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseDutchDate(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "31 februari 1980", "3 sept 1980", "gisteren"} {
		if _, err := ParseDutchDate(in); err == nil {
			t.Errorf("ParseDutchDate(%q) succeeded, want an error", in)
		}
	}
}
