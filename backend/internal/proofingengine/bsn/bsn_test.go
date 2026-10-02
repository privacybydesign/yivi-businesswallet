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
