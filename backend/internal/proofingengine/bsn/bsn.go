package bsn

import "strings"

const bsnLength = 9

func Mask(value string) string {
	// Already masked (a stored document's BSN masked again when the result
	// is built): leave it, rather than masking the mask.
	if strings.HasPrefix(value, "****.**.") {
		return value
	}
	digits := onlyDigits(value)
	if len(digits) != bsnLength {
		return strings.Repeat("*", len(value))
	}
	return "****.**." + digits[6:]
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
