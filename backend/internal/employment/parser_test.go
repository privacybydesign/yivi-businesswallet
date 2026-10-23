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
			"Werkgever/Instantie WW-Uitkering van UWV\nLoonheffingennummer 810220350L02\nVerzekerde wetten\nContractvorm\n" +
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
			From:         date("01-07-2026"), To: date("30-09-2026"), Worked: date("30-09-2026"), Kind: KindEmployment,
		},
		{
			Employer: "WW-Uitkering van UWV", PayrollTaxNumber: "810220350L02",
			From: date("01-01-2026"), To: date("31-01-2026"), Kind: KindBenefit,
		},
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

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		employer, number string
		want             Kind
	}{
		{"WW-Uitkering van UWV", "810220350L02", KindBenefit},
		{"WIA (WGA)-Uitkering van UWV", "810220350L26", KindBenefit},
		{"Toeslag bij Uitkering (TW) van UWV", "810220350L04", KindBenefit},
		{"UWV; Loondoorbetaling bij faillissement", "810220350L53", KindBenefit},
		// IBL 9.0.0 writes the descriptions in lower case.
		{"WW-uitkering van UWV", "810220350L02", KindBenefit},
		{"Ziektewet- of WAZO-uitkering van UWV", "810220350L20", KindBenefit},
		// The description alone is not enough, as in IBL: the number must match too.
		{"WW-Uitkering van UWV", "123456789L01", KindEmployment},
		{"UWV", "810220350L02", KindEmployment},
		{"Ziektewet- of WAZO-Uitkering van VOORBEELD B.V.", "123456789L01", KindSickViaEmployer},
		{"VOORBEELD B.V.", "123456789L01", KindEmployment},
	} {
		if got := classify(tc.employer, tc.number); got != tc.want {
			t.Errorf("classify(%q, %q) = %q, want %q", tc.employer, tc.number, got, tc.want)
		}
	}
}

func TestCurrent(t *testing.T) {
	job := func(employer, to, worked string) Relation {
		r := Relation{Employer: employer, To: date(to), Kind: KindEmployment}
		if worked != "" {
			r.Worked = date(worked)
		}
		return r
	}
	st := &Statement{Date: date("22-10-2026"), Relations: []Relation{
		job("SEPTEMBER", "30-09-2026", "30-09-2026"),
		job("AUGUST", "31-08-2026", "31-08-2026"),
		job("ON CALL, NO SHIFTS IN SEPTEMBER", "30-09-2026", "31-08-2026"),
		job("PAID WITHOUT HOURS", "30-09-2026", ""),
		job("JULY", "31-07-2026", "31-07-2026"),
		{Employer: "WW-Uitkering van UWV", To: date("30-09-2026"), Kind: KindBenefit},
		{Employer: "OLD BENEFIT", To: date("31-05-2026"), Kind: KindBenefit},
	}}
	var got []string
	for _, r := range st.Current() {
		got = append(got, r.Employer+"/"+string(r.Kind))
	}
	want := "SEPTEMBER/employment,AUGUST/employment,ON CALL, NO SHIFTS IN SEPTEMBER/employment," +
		"PAID WITHOUT HOURS/other_income,WW-Uitkering van UWV/benefit"
	if strings.Join(got, ",") != want {
		t.Errorf("Current = %v, want %s", got, want)
	}

	january := &Statement{Date: date("05-01-2027"), Relations: []Relation{
		job("NOVEMBER", "30-11-2026", "30-11-2026"),
		job("OCTOBER", "31-10-2026", "31-10-2026"),
	}}
	if cur := january.Current(); len(cur) != 1 || cur[0].Employer != "NOVEMBER" {
		t.Errorf("Current across a year = %+v, want November only", cur)
	}
	if cur := (&Statement{Date: date("22-10-2026")}).Current(); cur == nil || len(cur) != 0 {
		t.Errorf("Current without relations = %#v, want an empty list", cur)
	}
}

func TestEmployed(t *testing.T) {
	date22 := date("22-10-2026")
	for name, tc := range map[string]struct {
		relations []Relation
		want      bool
	}{
		"a job":                    {[]Relation{{Kind: KindEmployment, To: date("30-09-2026"), Worked: date("30-09-2026")}}, true},
		"sick via the employer":    {[]Relation{{Kind: KindSickViaEmployer, To: date("30-09-2026")}}, true},
		"only a benefit":           {[]Relation{{Kind: KindBenefit, To: date("30-09-2026")}}, false},
		"a past job":               {[]Relation{{Kind: KindEmployment, To: date("31-05-2026"), Worked: date("31-05-2026")}}, false},
		"paid but no hours":        {[]Relation{{Kind: KindEmployment, To: date("30-09-2026")}}, false},
		"a job and other income":   {[]Relation{{Kind: KindEmployment, To: date("30-09-2026")}, {Kind: KindEmployment, To: date("30-09-2026"), Worked: date("30-09-2026")}}, true},
		"a benefit and a job":      {[]Relation{{Kind: KindBenefit, To: date("30-09-2026")}, {Kind: KindEmployment, To: date("30-09-2026"), Worked: date("31-08-2026")}}, true},
		"nothing on the statement": {nil, false},
	} {
		if got := (&Statement{Date: date22, Relations: tc.relations}).Employed(); got != tc.want {
			t.Errorf("%s: Employed = %v, want %v", name, got, tc.want)
		}
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

// A month line straight after Contractvorm, without the Periode heading, is
// still a month and no part of the contract form.
func TestMonthsWithoutHeading(t *testing.T) {
	page := "Werkgever/Instantie VOORBEELD B.V.\nLoonheffingennummer 123456789L01\nContractvorm oproepovereenkomst\n" +
		"01-09-2026 t/m 30-09-2026 160 € 3.000,00\nVZB-006\n" + statementFooter(1, 1)
	st, err := parseStatement([]string{page})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Relations) != 1 || st.Relations[0].ContractForm != "oproepovereenkomst" || !st.Relations[0].To.Equal(date("30-09-2026")) {
		t.Errorf("relations = %+v, want one oproepovereenkomst reporting September", st.Relations)
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
