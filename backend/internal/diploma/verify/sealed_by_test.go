package verify

import "testing"

// SealedBy tells the issuer a file claims from its seal alone: a changed file
// still names its issuer, which PDF then refuses.
func TestSealedBy(t *testing.T) {
	p := newPKI(t, pkiOptions{})
	duo := signPDF(t, p, complete)
	uwv, _ := uwvPDF(t, p, p.seal.OCSPServer[0])
	changed := append([]byte{}, uwv...)
	changed[len("%PDF-1.")] = '4'

	for _, tc := range []struct {
		name             string
		pdf              []byte
		wantDUO, wantUWV bool
	}{
		{"a DUO seal", duo, true, false},
		{"a UWV seal", uwv, false, true},
		{"a changed UWV seal", changed, false, true},
		{"no signature", []byte("%PDF-1.7\n%%EOF\n"), false, false},
	} {
		if got := SealedBy(tc.pdf, testIssuer); got != tc.wantDUO {
			t.Errorf("%s: SealedBy DUO = %v, want %v", tc.name, got, tc.wantDUO)
		}
		if got := SealedBy(tc.pdf, testUWV); got != tc.wantUWV {
			t.Errorf("%s: SealedBy UWV = %v, want %v", tc.name, got, tc.wantUWV)
		}
	}
}
