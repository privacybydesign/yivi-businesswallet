package diploma

import (
	"slices"
	"testing"
	"time"
)

func TestMatchFullName(t *testing.T) {
	cases := []struct {
		name      string
		printed   string
		dob       string
		disclosed Person
		want      Result
	}{
		{
			name: "BRP style", printed: "Anna Maria van der Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "Anna Maria", Prefix: "van der", Surname: "Berg", DateOfBirth: "03-02-1980"},
			want:      Result{Matched: true, DateOfBirthMatch: true, SurnameMatch: true, GivenNamesMatch: true},
		},
		{
			name: "passport style, prefix in the last name", printed: "Anna Maria van der Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "ANNA", Surname: "VAN DER BERG", DateOfBirth: "1980-02-03"},
			want:      Result{Matched: true, DateOfBirthMatch: true, SurnameMatch: true, GivenNamesMatch: true},
		},
		{
			name: "trailing prefix", printed: "Anna van der Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "Anna", Surname: "Berg van der", DateOfBirth: "1980-02-03"},
			want:      Result{Matched: true, DateOfBirthMatch: true, SurnameMatch: true, GivenNamesMatch: true},
		},
		{
			name: "diacritics", printed: "Zoë Müller-Lüdenscheidt", dob: "1 januari 2000",
			disclosed: Person{GivenNames: "ZOE", Surname: "MULLER LUDENSCHEIDT", DateOfBirth: "2000-01-01"},
			want:      Result{Matched: true, DateOfBirthMatch: true, SurnameMatch: true, GivenNamesMatch: true},
		},
		{
			name: "other surname", printed: "Anna Maria van der Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "Anna", Surname: "Bergen", Prefix: "van der", DateOfBirth: "1980-02-03"},
			want:      Result{DateOfBirthMatch: true, GivenNamesMatch: true, Reasons: []string{"surname differs"}},
		},
		{
			name: "other given name", printed: "Anna Maria van der Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "Maria", Surname: "van der Berg", DateOfBirth: "1980-02-03"},
			want:      Result{DateOfBirthMatch: true, SurnameMatch: true, Reasons: []string{"given names differ"}},
		},
		{
			name: "only the surname printed", printed: "Berg", dob: "1980-02-03",
			disclosed: Person{GivenNames: "Anna", Surname: "Berg", DateOfBirth: "1980-02-03"},
			want:      Result{DateOfBirthMatch: true, Reasons: []string{"surname differs", "given names differ"}},
		},
		{
			name: "other date of birth", printed: "Piet Jansen", dob: "3 februari 1980",
			disclosed: Person{GivenNames: "Piet", Surname: "Jansen", DateOfBirth: "1980-02-04"},
			want:      Result{SurnameMatch: true, GivenNamesMatch: true, Reasons: []string{"date of birth differs"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchFullName(tc.printed, tc.dob, tc.disclosed)
			if got.Matched != tc.want.Matched || got.DateOfBirthMatch != tc.want.DateOfBirthMatch ||
				got.SurnameMatch != tc.want.SurnameMatch || got.GivenNamesMatch != tc.want.GivenNamesMatch ||
				!slices.Equal(got.Reasons, tc.want.Reasons) {
				t.Errorf("MatchFullName = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMatchFullNameUnreadableDate(t *testing.T) {
	got := MatchFullName("Piet Jansen", "onbekend", Person{GivenNames: "Piet", Surname: "Jansen", DateOfBirth: "1980-02-03"})
	if got.Matched || got.DateOfBirthMatch || len(got.Reasons) == 0 {
		t.Errorf("MatchFullName = %+v, want an unmatched date of birth with a reason", got)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"  van   der\tBerg ":  "VAN DER BERG",
		"Müller-Lüdenscheidt": "MULLER LUDENSCHEIDT",
		"O'Brien":             "O BRIEN",
		"":                    "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	want := time.Date(1980, 2, 3, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"1980-02-03", "03-02-1980", "3 februari 1980", "3 February 1980", "19800203", "03/02/1980", "1980-02-03T00:00:00Z"} {
		got, err := ParseDate(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseDate(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "14-13-1980", "yesterday"} {
		if _, err := ParseDate(in); err == nil {
			t.Errorf("ParseDate(%q) succeeded, want an error", in)
		}
	}
}
