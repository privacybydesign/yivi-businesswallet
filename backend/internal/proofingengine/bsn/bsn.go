package bsn

import "strings"

const bsnLength = 9

// maskedPrefix is what Mask puts before a BSN's last three digits.
const maskedPrefix = "****.**."

// elfproefModulus is the 11-proef's: a BSN's weighted digit sum is a multiple
// of it.
const elfproefModulus = 11

// lastDigitWeight is the 11-proef's weight of a BSN's last digit; the eight
// before it weigh 9 down to 2.
const lastDigitWeight = -1

// Valid reports whether value is a BSN: nine digits (dots and spaces aside)
// that pass the 11-proef, 9·d1 + 8·d2 + … + 2·d8 − d9 a multiple of 11 and
// not zero. DG11's personal number on a Dutch document is not always one, so
// a number that fails is not treated as a BSN.
func Valid(value string) bool {
	digits := onlyDigits(value)
	if len(digits) != bsnLength || len(digits) != len(strings.NewReplacer(".", "", " ", "").Replace(value)) {
		return false
	}
	sum := 0
	for i, r := range digits {
		weight := bsnLength - i
		if i == bsnLength-1 {
			weight = lastDigitWeight
		}
		sum += weight * int(r-'0')
	}
	return sum != 0 && sum%elfproefModulus == 0
}

// Masked reports whether value is already Mask's output.
func Masked(value string) bool {
	return strings.HasPrefix(value, maskedPrefix)
}

func Mask(value string) string {
	// Already masked (a stored document's BSN masked again when the result
	// is built): leave it, rather than masking the mask.
	if Masked(value) {
		return value
	}
	digits := onlyDigits(value)
	if len(digits) != bsnLength {
		return strings.Repeat("*", len(value))
	}
	return maskedPrefix + digits[6:]
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
