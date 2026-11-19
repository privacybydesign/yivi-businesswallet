package bsn

import "testing"

func TestMask(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain digits", "123456782", "****.**.782"},
		{"already dotted", "1234.56.782", "****.**.782"},
		{"spaced", "123 456 782", "****.**.782"},
		{"too short", "12345", "*****"},
		{"too long", "1234567890", "**********"},
		{"empty", "", ""},
		{"already masked", "****.**.782", "****.**.782"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Mask(c.in); got != c.want {
				t.Errorf("Mask(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", "123456782", true},
		{"valid dotted", "1234.56.782", true},
		{"valid spaced", "123 456 782", true},
		{"valid all nines and a zero", "999999990", true},
		{"checksum off by one", "123456783", false},
		{"all zeros", "000000000", false},
		{"too short", "12345678", false},
		{"too long", "1234567820", false},
		{"letters", "12345678X", false},
		{"masked", "****.**.782", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Valid(c.in); got != c.want {
				t.Errorf("Valid(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
