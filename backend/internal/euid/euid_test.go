package euid

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in    string
		want  EUID
		write string
	}{
		{"NLNHR.12345678", EUID{"NL", "NHR", "12345678", ""}, "NLNHR.12345678"},
		{"NLNHR.12345678.00001", EUID{"NL", "NHR", "12345678", "00001"}, "NLNHR.12345678.00001"},
		{"  NLNHR.12345678 ", EUID{"NL", "NHR", "12345678", ""}, "NLNHR.12345678"},
		{"DEHRB.ab12", EUID{"DE", "HRB", "ab12", ""}, "DEHRB.ab12"},
		{"DEHRB.ab12.X1", EUID{"DE", "HRB", "ab12", "X1"}, "DEHRB.ab12.X1"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if got.String() != tt.write {
				t.Fatalf("String() = %q, want %q", got.String(), tt.write)
			}
			again, err := Parse(got.String())
			if err != nil || again != got {
				t.Fatalf("round trip of %q = %+v, %v", got.String(), again, err)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"12345678",
		"NLNHR",
		"NLNHR.",
		"NLNHR.12345678.",
		"NLNHR.1234.5678.9",
		"nlnhr.12345678",
		"NLNHR.1234567",
		"NLNHR.123456789",
		"NLNHR.1234567A",
		"NLKVK.12345678",
		"NL.KVK.12345678",
		"NL.NHR.12345678",
		"NL-NHR-12345678",
		"DE-HRB-123456",
		"DE.HRB.12345",
		"DEB8534.HRB14000",
		"DEHR.12345",
		"DEHRB.12 345",
		"DEHRB.12345.0-1",
		"1EHRB.12345",
	} {
		if _, err := Parse(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", in, err)
		}
	}
}

func TestISO6523(t *testing.T) {
	for _, in := range []string{"NLNHR.12345678", "NLNHR.12345678.00001"} {
		e, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		p, err := e.ISO6523()
		if err != nil {
			t.Fatalf("ISO6523(%q): %v", in, err)
		}
		if p.String() != "0106:12345678" {
			t.Errorf("ISO6523(%q) = %q, want 0106:12345678", in, p)
		}
	}

	de, err := Parse("DEHRB.123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := de.ISO6523(); !errors.Is(err, ErrNoICD) {
		t.Errorf("ISO6523(DE) error = %v, want ErrNoICD", err)
	}
}

func TestParseISO6523(t *testing.T) {
	for _, in := range []string{"0106:12345678", "iso6523:0106:12345678", "ebcore:iso6523:0106:12345678", "urn:oasis:names:tc:ebcore:partyid-type:iso6523:0106:12345678"} {
		p, err := ParseISO6523(in)
		if err != nil {
			t.Fatalf("ParseISO6523(%q): %v", in, err)
		}
		e, err := p.EUID()
		if err != nil {
			t.Fatalf("EUID(%q): %v", in, err)
		}
		if e.String() != "NLNHR.12345678" {
			t.Errorf("EUID(%q) = %q, want NLNHR.12345678", in, e)
		}
	}

	for _, in := range []string{"", "0106", "106:12345678", "0106:", "0106:12 34"} {
		if _, err := ParseISO6523(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseISO6523(%q) error = %v, want ErrInvalid", in, err)
		}
	}

	if _, err := (ISO6523{ICD: "9999", ID: "1"}).EUID(); !errors.Is(err, ErrNoICD) {
		t.Errorf("unknown ICD error = %v, want ErrNoICD", err)
	}
	if _, err := (ISO6523{ICD: icdKVK, ID: "123"}).EUID(); !errors.Is(err, ErrInvalid) {
		t.Errorf("short KVK number error = %v, want ErrInvalid", err)
	}
}
