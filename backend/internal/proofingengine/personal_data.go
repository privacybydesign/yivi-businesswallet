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
// The registered BSN, when disclosed, is the document's personal number: the
// BRP's own, over whatever the photographed document prints.
func disclosedDocument(sess session.Session, info disclosureInfo, bsnPolicy privacy.BSNPolicy, now time.Time) (documentInfo, bool) {
	if !disclosesPersonalData(info) {
		doc := documentFromDisclosure(info)
		if scanned, ok := photographedDocument(sess, bsnPolicy, now); ok {
			doc.PersonalNumber = scanned.PersonalNumber
		}
		return doc, true
	}
	doc, checks := personalDataDocument(sess, info, bsnPolicy, now)
	if registered := registeredBSN(info, bsnPolicy); registered != "" {
		doc.PersonalNumber = registered
	}
	return doc, checks
}

// personalDataDocument is the document of a personal data disclosure: the
// photographed one, or without a usable photo the disclosure's own.
func personalDataDocument(sess session.Session, info disclosureInfo, bsnPolicy privacy.BSNPolicy, now time.Time) (documentInfo, bool) {
	if doc, ok := photographedDocument(sess, bsnPolicy, now); ok {
		return doc, true
	}
	return documentFromDisclosure(info), false
}

// photographedDocument is what the session's document photo read, its
// personal number under the BSN policy and only when DG11 is requested; ok is
// false without a usable photo.
func photographedDocument(sess session.Session, bsnPolicy privacy.BSNPolicy, now time.Time) (documentInfo, bool) {
	p := sess.Steps.DocumentPhoto
	if p == nil || p.Check == nil {
		return documentInfo{}, false
	}
	scanned := scannedDocumentInfo(p.Check.Document, now)
	if scanned == nil {
		return documentInfo{}, false
	}
	doc := *scanned
	applyBSNPolicy(&doc, bsnPolicy)
	if !attrRequested(sess, attrDG11) {
		doc.PersonalNumber = ""
	}
	return doc, true
}

// personalDataBSN is the personal data's BSN claim.
const personalDataBSN = "bsn"

// withholdDisclosedBSN keeps the disclosure's own BSN only as the flow's
// policy releases it, since the disclosure goes in the result next to the
// document.
func withholdDisclosedBSN(info *disclosureInfo, policy privacy.BSNPolicy) {
	if _, ok := info.Attributes[personalDataBSN]; !ok {
		return
	}
	if released := registeredBSN(*info, policy); released != "" {
		info.Attributes[personalDataBSN] = released
		return
	}
	delete(info.Attributes, personalDataBSN)
}

// registeredBSN is the BSN the personal data disclosed, under the flow's BSN
// policy: empty when none was disclosed, it is no valid BSN or the policy
// omits it.
func registeredBSN(info disclosureInfo, policy privacy.BSNPolicy) string {
	doc := documentInfo{PersonalNumber: info.Attributes[personalDataBSN], IssuingState: dutchIssuingState}
	applyBSNPolicy(&doc, policy)
	return doc.PersonalNumber
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
