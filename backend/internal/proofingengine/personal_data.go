package proofingengine

import (
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A Yivi disclosure of the registered personal data (a flow accepting
// flow.DocumentKindPersonalData) carries no document: the document is the one
// photographed in the browser, tied to the person by the holder's name and
// date of birth.

// personalDataCredential is the last segment of the personal data's vct
// (pbdf.gemeente.personalData).
const personalDataCredential = "personalData"

// disclosesPersonalData reports whether info is the registered personal data
// rather than a document.
func disclosesPersonalData(info disclosureInfo) bool {
	return info.Credential[strings.LastIndex(info.Credential, ".")+1:] == personalDataCredential
}

// reasonPersonalDataMismatch: the photographed document's surname is not the
// registered one, which a reviewer decides (a partner's name, a truncated MRZ).
const reasonPersonalDataMismatch = "personal_data_mismatch"

// disclosedDocument is the session's document: the disclosed one, or for
// personal data the photographed one, with the BSN as the flow releases it.
// checks is false when there is no document to hold to the flow's document
// rules: personal data without a document photo.
func disclosedDocument(sess session.Session, info disclosureInfo, bsnPolicy privacy.BSNPolicy, now time.Time) (doc documentInfo, checks bool) {
	if !disclosesPersonalData(info) {
		return documentFromDisclosure(info), true
	}
	p := sess.Steps.DocumentPhoto
	if p == nil || p.Check == nil {
		return documentFromDisclosure(info), false
	}
	scanned := scannedDocumentInfo(p.Check.Document, now)
	if scanned == nil {
		return documentFromDisclosure(info), false
	}
	doc = *scanned
	applyBSNPolicy(&doc, bsnPolicy)
	if !attrRequested(sess, attrDG11) {
		doc.PersonalNumber = ""
	}
	return doc, true
}

// personalDataMatch holds the photographed document to the disclosed personal
// data: another date of birth, or none disclosed, is another person
// (DOCUMENT_MISMATCH); a date the scan did not read, or the same date with
// another surname, is a mismatch a reviewer decides. A session without a
// document photo has nothing to match.
func personalDataMatch(sess session.Session, info disclosureInfo) (errorCode string, mismatch bool) {
	p := sess.Steps.DocumentPhoto
	if p == nil {
		return "", false
	}
	if p.Check == nil {
		return errCodeDocMismatch, false
	}
	scanned := p.Check.Document
	registered := documentFromDisclosure(info)
	born, ok := disclosedDate(registered.DateOfBirth)
	if !ok {
		return errCodeDocMismatch, false
	}
	scannedBorn, ok := disclosedDate(scanned.DateOfBirth)
	if !ok {
		return "", true
	}
	if !born.Equal(scannedBorn) {
		return errCodeDocMismatch, false
	}
	return "", !sameSurname(registered.LastName, scanned.Surname)
}

// sameSurname is whether a and b are one surname, written by the BRP and read
// off a document: case, accents, spaces and punctuation aside, so "de Vries"
// is "DE<VRIES" and "Müller" is "MULLER".
func sameSurname(a, b string) bool {
	na, nb := nameKey(a), nameKey(b)
	return na != "" && na == nb
}

func nameKey(s string) string {
	folded, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		folded = s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, folded)
}
