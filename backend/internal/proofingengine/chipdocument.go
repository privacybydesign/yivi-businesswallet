package proofingengine

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gmrtd/gmrtd/document"
	"github.com/gmrtd/gmrtd/mrz"
	"github.com/gmrtd/gmrtd/tlv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
)

// documentTypeDriversLicense is documentInfo.Type for an EU driving licence,
// which has no MRZ document code; the app reports the same value, and so does
// a chip access key (validChipAccessDocumentType). The steps refuse a licence
// for now (flow.DrivingLicence).
const documentTypeDriversLicense = flow.DocumentTypeDrivingLicence

// The ISO/IEC 18013-2 tags of an EU driving licence's DG1: the data group,
// its personal-data container, and the fields read from it.
const (
	edlDG1Tag            tlv.TlvTag = 0x61
	edlPersonalDataTag   tlv.TlvTag = 0x5F02
	edlIssuingStateTag   tlv.TlvTag = 0x5F03
	edlSurnameTag        tlv.TlvTag = 0x5F04
	edlOtherNamesTag     tlv.TlvTag = 0x5F05
	edlDateOfBirthTag    tlv.TlvTag = 0x5F06
	edlPlaceOfBirthTag   tlv.TlvTag = 0x5F07
	edlDateOfExpiryTag   tlv.TlvTag = 0x5F0B
	edlDocumentNumberTag tlv.TlvTag = 0x5F0E
)

// mrzDateLength is an MRZ date's YYMMDD; fullDateLength is DG11's YYYYMMDD
// and a driving licence's BCD DDMMYYYY once hex-encoded.
const (
	mrzDateLength  = 6
	fullDateLength = 8
)

// expiryCenturyWindow is how far ahead an MRZ expiry's two-digit year may
// lie before it is read as the previous century.
const expiryCenturyWindow = 50

// documentFromEvidence reads the holder's identity off the chip evidence
// itself: DG1 (and DG11 when read) of a passport or identity card, DG1 of an
// EU driving licence. It is what a session's document is, never the
// document the app sends alongside: that is the app's own re-serialisation,
// which nothing binds to the chip, so a modified app could pair a genuine
// chip read with someone else's name. Passive Authentication
// (verifyMrtdEvidence) hash-checks these same bytes against EF.SOD; this
// only parses them. An MRZ that parses has passed its check digits
// (mrz.MrzDecode refuses one that does not).
func documentFromEvidence(ev *mrtdEvidenceRequest, now time.Time) (*documentInfo, error) {
	dg1, err := dataGroupBytes(ev, dataGroupMRZ)
	if err != nil {
		return nil, err
	}
	if ev.DocumentType == documentTypeEUDrivingLicence {
		return drivingLicenceDocument(dg1)
	}
	return icaoDocument(dg1, ev, now)
}

// dataGroupBytes is ev's data group name, which must be present.
func dataGroupBytes(ev *mrtdEvidenceRequest, name string) ([]byte, error) {
	raw, ok := ev.DataGroups[name]
	if !ok {
		return nil, fmt.Errorf("%s is required to read the document", name)
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not hex: %w", name, err)
	}
	return b, nil
}

// icaoDocument reads a passport's or identity card's DG1 MRZ, and DG11 for
// the full name, date of birth, personal number and place of birth.
func icaoDocument(dg1Bytes []byte, ev *mrtdEvidenceRequest, now time.Time) (*documentInfo, error) {
	dg1, err := document.NewDG1(dg1Bytes)
	if err != nil {
		return nil, fmt.Errorf("DG1: %w", err)
	}
	if dg1 == nil || dg1.Mrz == nil {
		return nil, errors.New("DG1 has no MRZ")
	}
	m := dg1.Mrz
	given, surname := mrzNameParts(m.NameOfHolder)
	birth, err := mrzDate(m.DateOfBirth, now, false)
	if err != nil {
		return nil, fmt.Errorf("DG1 date of birth: %w", err)
	}
	expiry, err := mrzDate(m.DateOfExpiry, now, true)
	if err != nil {
		return nil, fmt.Errorf("DG1 date of expiry: %w", err)
	}
	doc := &documentInfo{
		Type: m.DocumentCode, Number: m.DocumentNumber, IssuingState: m.IssuingState, Nationality: m.Nationality,
		FirstName: given, LastName: surname, DisplayName: displayName(given, surname), Sex: m.Sex,
		DateOfBirth: birth, DateOfExpiry: expiry,
	}
	if _, ok := ev.DataGroups[dataGroupPersonalDetails]; !ok {
		return doc, nil
	}
	dg11Bytes, err := dataGroupBytes(ev, dataGroupPersonalDetails)
	if err != nil {
		return nil, err
	}
	dg11, err := document.NewDG11(dg11Bytes)
	if err != nil {
		return nil, fmt.Errorf("DG11: %w", err)
	}
	if dg11 == nil {
		return doc, nil
	}
	details := dg11.Details
	// DG11's name is UTF-8 and keeps diacritics the MRZ transliterates away.
	if g, s := mrzNameParts(details.NameOfHolder); s != "" {
		doc.DisplayName = displayName(g, s)
	}
	if full, ok := fullDate(details.FullDateOfBirth); ok {
		doc.DateOfBirth = full
	}
	doc.PersonalNumber = details.PersonalNumber
	doc.PlaceOfBirth = strings.Join(details.PlaceOfBirth, ", ")
	return doc, nil
}

// drivingLicenceDocument reads an EU driving licence's DG1 (ISO/IEC
// 18013-2): its personal-data container 5F02 holds the fields as TLVs of
// their own, though the tag is not marked constructed.
func drivingLicenceDocument(dg1 []byte) (*documentInfo, error) {
	nodes, err := tlv.Decode(dg1)
	if err != nil {
		return nil, fmt.Errorf("DG1: %w", err)
	}
	personal := nodes.NodeByTag(edlDG1Tag).NodeByTag(edlPersonalDataTag)
	if !personal.IsValidNode() {
		return nil, errors.New("DG1 has no driving licence personal data (5F02)")
	}
	fields, err := tlv.Decode(personal.Value())
	if err != nil {
		return nil, fmt.Errorf("DG1 personal data: %w", err)
	}
	text := func(tag tlv.TlvTag) string { return latin1(fields.NodeByTag(tag).Value()) }
	birth, ok := bcdDate(fields.NodeByTag(edlDateOfBirthTag).Value())
	if !ok {
		return nil, errors.New("DG1 date of birth is not a BCD date")
	}
	expiry, ok := bcdDate(fields.NodeByTag(edlDateOfExpiryTag).Value())
	if !ok {
		return nil, errors.New("DG1 date of expiry is not a BCD date")
	}
	surname, given := text(edlSurnameTag), text(edlOtherNamesTag)
	if surname == "" {
		return nil, errors.New("DG1 has no holder surname")
	}
	return &documentInfo{
		Type: documentTypeDriversLicense, Number: text(edlDocumentNumberTag), IssuingState: text(edlIssuingStateTag),
		FirstName: given, LastName: surname, DisplayName: displayName(given, surname),
		DateOfBirth: birth, DateOfExpiry: expiry, PlaceOfBirth: text(edlPlaceOfBirthTag),
	}, nil
}

// mrzNameParts is an MRZ-style name's given names and surname (primary).
func mrzNameParts(n *mrz.MrzName) (given, surname string) {
	if n == nil {
		return "", ""
	}
	return strings.TrimSpace(n.Secondary), strings.TrimSpace(n.Primary)
}

// mrzDate turns an MRZ YYMMDD into YYYY-MM-DD. A date of birth is never in
// the future, so one after today is the previous century; an expiry is this
// century unless that puts it more than expiryCenturyWindow years ahead.
func mrzDate(yymmdd string, now time.Time, expiry bool) (string, error) {
	if len(yymmdd) != mrzDateLength {
		return "", fmt.Errorf("%q is not YYMMDD", yymmdd)
	}
	yy, err := strconv.Atoi(yymmdd[:2])
	if err != nil {
		return "", fmt.Errorf("%q is not YYMMDD", yymmdd)
	}
	year := now.Year()/100*100 + yy
	if expiry && year > now.Year()+expiryCenturyWindow {
		year -= 100
	}
	d, err := checkedDate(fmt.Sprintf("%04d%s", year, yymmdd[2:]))
	if err != nil || expiry || d <= now.Format(time.DateOnly) {
		return d, err
	}
	return checkedDate(fmt.Sprintf("%04d%s", year-100, yymmdd[2:]))
}

// fullDate turns DG11's YYYYMMDD into YYYY-MM-DD; ok is false for anything
// else (DG11 dates may be partial, e.g. an unknown day).
func fullDate(yyyymmdd string) (string, bool) {
	if len(yyyymmdd) != fullDateLength {
		return "", false
	}
	d, err := checkedDate(yyyymmdd)
	return d, err == nil
}

// bcdDate turns a driving licence's BCD DDMMYYYY into YYYY-MM-DD.
func bcdDate(b []byte) (string, bool) {
	ddmmyyyy := hex.EncodeToString(b)
	if len(ddmmyyyy) != fullDateLength {
		return "", false
	}
	d, err := checkedDate(ddmmyyyy[4:] + ddmmyyyy[2:4] + ddmmyyyy[:2])
	return d, err == nil
}

// checkedDate is YYYYMMDD as YYYY-MM-DD, if it is a real calendar date.
func checkedDate(yyyymmdd string) (string, error) {
	t, err := time.Parse("20060102", yyyymmdd)
	if err != nil {
		return "", err
	}
	return t.Format(time.DateOnly), nil
}

// latin1 decodes ISO/IEC 8859-1 bytes, as a driving licence's DG1 text is.
func latin1(b []byte) string {
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return strings.TrimSpace(string(runes))
}
