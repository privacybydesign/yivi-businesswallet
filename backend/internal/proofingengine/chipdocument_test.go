package proofingengine

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify/mrtdtestfixtures"
)

// tlvBytes encodes one BER-TLV with a short-form length.
func tlvBytes(tag []byte, value ...[]byte) []byte {
	var v []byte
	for _, part := range value {
		v = append(v, part...)
	}
	out := append([]byte{}, tag...)
	out = append(out, byte(len(v)))
	return append(out, v...)
}

// The ICAO fixture's DG1 is read for the document: its MRZ name, number and
// dates, not anything the app might claim.
func TestDocumentFromEvidenceICAO(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	doc, err := documentFromEvidence(&mrtdEvidenceRequest{
		DataGroups: map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex},
	}, now)
	if err != nil {
		t.Fatalf("documentFromEvidence: %v", err)
	}
	if doc.Type != "P" || doc.IssuingState != "GBR" || doc.Nationality != "GBR" || doc.Number != "099250692" ||
		doc.LastName != "SANDERSON" || doc.FirstName != "OSCAR CHARLES EDWARD" ||
		doc.DisplayName != "OSCAR CHARLES EDWARD SANDERSON" || doc.Sex != "M" ||
		doc.DateOfBirth != "1978-04-05" || doc.DateOfExpiry != "2022-07-17" {
		t.Errorf("document = %+v; want the fixture's MRZ", doc)
	}
}

// An EU driving licence's DG1 is read from its ISO 18013-2 fields.
func TestDocFromEvidenceLicence(t *testing.T) {
	personal := tlvBytes([]byte{0x5F, 0x02},
		tlvBytes([]byte{0x5F, 0x03}, []byte("NLD")),
		tlvBytes([]byte{0x5F, 0x04}, []byte("JANSEN")),
		tlvBytes([]byte{0x5F, 0x05}, []byte{'R', 'e', 'n', 0xE9}), // "René" in Latin-1
		tlvBytes([]byte{0x5F, 0x06}, []byte{0x15, 0x03, 0x19, 0x90}),
		tlvBytes([]byte{0x5F, 0x07}, []byte("AMSTERDAM")),
		tlvBytes([]byte{0x5F, 0x0B}, []byte{0x01, 0x01, 0x20, 0x30}),
		tlvBytes([]byte{0x5F, 0x0E}, []byte("5012345678")),
	)
	dg1 := tlvBytes([]byte{0x61}, tlvBytes([]byte{0x5F, 0x01}, []byte("e1")), personal)
	doc, err := documentFromEvidence(&mrtdEvidenceRequest{
		DocumentType: documentTypeEUDrivingLicence,
		DataGroups:   map[string]string{"DG1": hex.EncodeToString(dg1)},
	}, time.Now())
	if err != nil {
		t.Fatalf("documentFromEvidence: %v", err)
	}
	if doc.Type != documentTypeDriversLicense || doc.IssuingState != "NLD" || doc.Number != "5012345678" ||
		doc.LastName != "JANSEN" || doc.FirstName != "René" || doc.DateOfBirth != "1990-03-15" ||
		doc.DateOfExpiry != "2030-01-01" || doc.PlaceOfBirth != "AMSTERDAM" {
		t.Errorf("document = %+v; want the licence's DG1", doc)
	}
}

// Without a readable DG1 there is no document: the app's copy is never a
// fallback.
func TestDocFromEvidenceNeedsDG1(t *testing.T) {
	for name, groups := range map[string]map[string]string{
		"no DG1":     {"DG2": mrtdtestfixtures.Dg2Hex},
		"not hex":    {"DG1": "zz"},
		"not an MRZ": {"DG1": hex.EncodeToString(tlvBytes([]byte{0x61}, tlvBytes([]byte{0x5F, 0x1F}, []byte("P<GBR"))))},
	} {
		if _, err := documentFromEvidence(&mrtdEvidenceRequest{DataGroups: groups}, time.Now()); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A passport's DG1 claimed as a driving licence has no 5F02.
	if _, err := documentFromEvidence(&mrtdEvidenceRequest{
		DocumentType: documentTypeEUDrivingLicence,
		DataGroups:   map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex},
	}, time.Now()); err == nil {
		t.Error("passport DG1 as a driving licence: want an error")
	}
}

// A two-digit birth year is never in the future; an expiry year is this
// century unless that is implausibly far ahead.
func TestMRZDate(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		kind mrzDateKind
		want string
	}{
		{"780405", mrzBirthDate, "1978-04-05"},
		{"050101", mrzBirthDate, "2005-01-01"},
		{"261231", mrzBirthDate, "1926-12-31"},
		{"260101", mrzBirthDate, "2026-01-01"},
		{"340717", mrzExpiryDate, "2034-07-17"},
		{"990101", mrzExpiryDate, "1999-01-01"},
	}
	for _, c := range cases {
		if got, err := mrzDate(c.in, now, c.kind); err != nil || got != c.want {
			t.Errorf("mrzDate(%q, kind=%v) = %q, %v; want %q", c.in, c.kind, got, err, c.want)
		}
	}
	if _, err := mrzDate("781305", now, mrzBirthDate); err == nil {
		t.Error("month 13: want an error")
	}
}

// mrzCheckDigit is ICAO 9303's check digit over s.
func mrzCheckDigit(s string) string {
	weights := []int{7, 3, 1}
	sum := 0
	for i, r := range s {
		v := 0
		switch {
		case r >= '0' && r <= '9':
			v = int(r - '0')
		case r >= 'A' && r <= 'Z':
			v = int(r-'A') + 10
		}
		sum += v * weights[i%len(weights)]
	}
	return string(rune('0' + sum%10))
}

// mrzField pads s with fillers to n characters.
func mrzField(s string, n int) string {
	return s + strings.Repeat("<", n-len(s))
}

// td1DG1 is the DG1 of a card-sized (TD1) document with this code, issuing
// state and nationality.
func td1DG1(code, issuingState, nationality string) []byte {
	const number, birth, expiry = "SPECI2014", "650310", "340309"
	line1 := mrzField(code, 2) + issuingState + number + mrzCheckDigit(number) + mrzField("", 15)
	line2 := birth + mrzCheckDigit(birth) + "F" + expiry + mrzCheckDigit(expiry) + nationality + mrzField("", 11)
	line2 += mrzCheckDigit(line1[5:30] + line2[0:7] + line2[8:15] + line2[18:29])
	line3 := mrzField("DE<BRUIJN<<WILLEKE<LISELOTTE", 30)
	return tlvBytes([]byte{0x61}, tlvBytes([]byte{0x5F, 0x1F}, []byte(line1+line2+line3)))
}

// A TD1 card's DG1 gives its kind: a residence permit by its code, an
// identity card by holder and issuer matching, and neither for another
// state's card.
func TestDocFromEvidenceTD1Kind(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name                      string
		code, issuer, nationality string
		want                      string
	}{
		{"residence permit", "IR", "NLD", "TUR", flow.DocumentKindResidencePermit},
		{"another state's card", "ID", "BEL", "MAR", ""},
		{"identity card", "I", "NLD", "NLD", flow.DocumentKindIdentityCard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := documentFromEvidence(&mrtdEvidenceRequest{
				DataGroups: map[string]string{"DG1": hex.EncodeToString(td1DG1(tc.code, tc.issuer, tc.nationality))},
			}, now)
			if err != nil {
				t.Fatalf("documentFromEvidence: %v", err)
			}
			if doc.Kind != tc.want || doc.IssuingState != tc.issuer || doc.Nationality != tc.nationality ||
				doc.LastName != "DE BRUIJN" || doc.DateOfBirth != "1965-03-10" {
				t.Errorf("document = %+v; want kind %q from the MRZ", doc, tc.want)
			}
		})
	}
}
