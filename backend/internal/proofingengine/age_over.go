package proofingengine

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// ageOverInfo answers attrAgeOver: whether the holder is Age or older, without
// the date of birth it comes from.
type ageOverInfo = session.AgeOver

// asksAge reports whether fd is an age-check flow, which never stores the
// date of birth.
func asksAge(fd *flow.FlowDefinition) bool {
	return fd != nil && fd.AgeOver != 0
}

// ageOverOf is fd's age answered from doc, the document read off the chip, on
// the UTC date of now; nil when fd asks none. The chip reader refuses a
// partial date of birth, so one that came off the chip parses.
func ageOverOf(doc *documentInfo, fd *flow.FlowDefinition, now time.Time) *ageOverInfo {
	if !asksAge(fd) || doc == nil {
		return nil
	}
	born, err := time.Parse(time.DateOnly, doc.DateOfBirth)
	if err != nil {
		return nil
	}
	return &ageOverInfo{Age: fd.AgeOver, Over: reachedAge(born, fd.AgeOver, now)}
}

// withholdBirthDate drops the date of birth from req before it is stored: the
// parsed date, and the raw DG1 and DG11 that carry it.
func withholdBirthDate(req *submitNFCStepRequest) {
	if req.Document != nil {
		req.Document.DateOfBirth = ""
	}
	if req.MrtdEvidence != nil {
		dropDataGroups(req.MrtdEvidence, dataGroupMRZ, dataGroupPersonalDetails)
	}
}

// yiviAgeClaimPrefix names the Yivi passport and id-card claims ("Yes"/"No")
// that answer whether the holder is an age or older: over18 for 18.
const yiviAgeClaimPrefix = "over"

// yiviBirthDateClaims are the Yivi passport and id-card claims that carry the
// date of birth, or part of it.
var yiviBirthDateClaims = []string{"dateOfBirth", "yearOfBirth"}

// disclosedAgeOver answers age from a Yivi disclosure's age claim; nil when it
// carries none.
func disclosedAgeOver(attributes map[string]string, age int) *ageOverInfo {
	switch strings.ToLower(attributes[yiviAgeClaimPrefix+strconv.Itoa(age)]) {
	case "yes", "true":
		return &ageOverInfo{Age: age, Over: true}
	case "no", "false":
		return &ageOverInfo{Age: age, Over: false}
	}
	return nil
}

// withholdDisclosedBirthDate drops the date of birth from info before it is
// stored, should the Yivi app have disclosed it anyway.
func withholdDisclosedBirthDate(info *disclosureInfo) {
	for _, claim := range yiviBirthDateClaims {
		delete(info.Attributes, claim)
	}
	info.AttributeIDs = slices.DeleteFunc(info.AttributeIDs, func(id string) bool {
		return slices.ContainsFunc(yiviBirthDateClaims, func(claim string) bool { return strings.HasSuffix(id, "."+claim) })
	})
}

// reachedAge reports whether someone born on born is age or older on the UTC
// date of now. Born on 29 February, they reach it on 1 March of a common year.
func reachedAge(born time.Time, age int, now time.Time) bool {
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return !today.Before(born.AddDate(age, 0, 0))
}
