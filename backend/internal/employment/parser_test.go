package employment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// statementFooter is what UWV prints at the end of every page, for a
// made-up holder.
func statementFooter(page, pages int) string {
	return strings.Join([]string{
		"Mevrouw J.Th. Jansen-de Vries",
		"Geboortedatum: 03-02-1980 Datum: 15 oktober 2026",
		"Printversie verzekeringsbericht",
		fmt.Sprintf("Pagina %d van %d", page, pages),
	}, "\n")
}

// syntheticStatement mimics a real statement's text: an "Arbeidsverleden"
// page, then relation blocks, one continued on the next page with its labels
// repeated, a month line carrying the form code, and the disclaimer.
func syntheticStatement() []string {
	return []string{
		"Arbeidsverleden\nUw totale arbeidsverleden\n12 jaar tot 1998\nVZB-006\n" + statementFooter(1, 4),
		"Loongegevens\nWerkgever/Instantie VOORBEELD B.V.\nLoonheffingennummer 123456789L01\n" +
			"Verzekerde wetten WW, ZW en WIA\nContractvorm schriftelijke arbeidsovereenkomst voor bepaalde\n" +
			"tijd, geen oproepovereenkomst\nPeriode Aantal uur Sv-loon\n" +
			"01-09-2026 t/m 30-09-2026 160 € 3.000,00\n01-08-2026 t/m 31-08-2026 160 € 3.000,00 VZB-006\n" +
			statementFooter(2, 4),
		"Werkgever/Instantie VOORBEELD B.V.\nLoonheffingennummer 123456789L01\nVerzekerde wetten WW, ZW en WIA\n" +
			"Contractvorm schriftelijke arbeidsovereenkomst voor bepaalde\ntijd, geen oproepovereenkomst\n" +
			"Periode Aantal uur Sv-loon\n01-07-2026 t/m 31-07-2026 160 € 3.000,00\n" +
			"Werkgever/Instantie UWV\nLoonheffingennummer 987654321L01\nVerzekerde wetten\nContractvorm\n" +
			"Periode Aantal uur Sv-loon\n01-01-2026 t/m 31-01-2026 0 € 900,00 VZB-006\n" + statementFooter(3, 4),
		"Werkgever/Instantie LEEG B.V.\nLoonheffingennummer 111111111L01\nContractvorm\nPeriode Aantal uur Sv-loon\n" +
			"Disclaimer: Dit document bevat gegevens die op de datum van de aanmaak van het document bij UWV bekend zijn.\n" +
			"VZB-006\n" + statementFooter(4, 4),
	}
}

func date(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseStatement(t *testing.T) {
	st, err := parseStatement(syntheticStatement())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.Initials, " ") != "J Th" || st.Surname != "Jansen-de Vries" {
		t.Errorf("holder = %v %q, want [J Th] \"Jansen-de Vries\"", st.Initials, st.Surname)
	}
	if !st.DateOfBirth.Equal(date("03-02-1980")) || !st.Date.Equal(date("15-10-2026")) {
		t.Errorf("dates = %s, %s", st.DateOfBirth, st.Date)
	}
	want := []Relation{
		{
			Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01",
			ContractForm: "schriftelijke arbeidsovereenkomst voor bepaalde tijd, geen oproepovereenkomst",
			From:         date("01-07-2026"), To: date("30-09-2026"),
		},
		{Employer: "UWV", PayrollTaxNumber: "987654321L01", From: date("01-01-2026"), To: date("31-01-2026")},
	}
	if len(st.Relations) != len(want) {
		t.Fatalf("relations = %+v, want %+v", st.Relations, want)
	}
	for i := range want {
		if st.Relations[i] != want[i] {
			t.Errorf("relation %d = %+v, want %+v", i, st.Relations[i], want[i])
		}
	}
}

func TestCurrent(t *testing.T) {
	st := &Statement{Date: date("22-10-2026"), Relations: []Relation{
		{Employer: "SEPTEMBER", From: date("01-09-2026"), To: date("30-09-2026")},
		{Employer: "AUGUST", From: date("01-01-2025"), To: date("31-08-2026")},
		{Employer: "JULY", From: date("01-02-2026"), To: date("31-07-2026")},
		{Employer: "LONG AGO", From: date("01-02-2025"), To: date("30-06-2025")},
	}}
	var got []string
	for _, r := range st.Current() {
		got = append(got, r.Employer)
	}
	if strings.Join(got, ",") != "SEPTEMBER,AUGUST" {
		t.Errorf("Current = %v, want the relations reporting August or later", got)
	}

	january := &Statement{Date: date("05-01-2027"), Relations: []Relation{
		{Employer: "NOVEMBER", To: date("30-11-2026")},
		{Employer: "OCTOBER", To: date("31-10-2026")},
	}}
	if cur := january.Current(); len(cur) != 1 || cur[0].Employer != "NOVEMBER" {
		t.Errorf("Current across a year = %+v, want November only", cur)
	}
	if cur := (&Statement{Date: date("22-10-2026")}).Current(); cur == nil || len(cur) != 0 {
		t.Errorf("Current without relations = %#v, want an empty list", cur)
	}
}

func TestParseStatementRefuses(t *testing.T) {
	pages := syntheticStatement()
	otherHolder := append([]string{}, pages...)
	otherHolder[1] = strings.Replace(otherHolder[1], "Jansen-de Vries", "Smit", 1)

	for name, pages := range map[string][]string{
		"no footer":        {"Diploma\nBEHAALD DOOR\nAnna"},
		"no form code":     {strings.ReplaceAll(strings.Join(pages, "\n"), "VZB-006", "")},
		"pages disagree":   otherHolder,
		"no initials":      {strings.Replace(pages[0], "J.Th. ", "", 1)},
		"no surname":       {strings.Replace(pages[0], "J.Th. Jansen-de Vries", "J.Th.", 1)},
		"unreadable dates": {strings.Replace(pages[0], "15 oktober 2026", "15 brumaire 2026", 1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseStatement(pages); !errors.Is(err, ErrNotAStatement) {
				t.Errorf("err = %v, want ErrNotAStatement", err)
			}
		})
	}
}

type fakeText struct {
	pages []string
	err   error
}

func (f fakeText) Text(context.Context, []byte) ([]string, error) { return f.pages, f.err }

func TestTextParserUnreadable(t *testing.T) {
	_, err := NewTextParser(fakeText{err: diploma.ErrUnreadable}).Parse(context.Background(), nil)
	if !errors.Is(err, ErrNotAStatement) {
		t.Errorf("err = %v, want ErrNotAStatement", err)
	}
}
