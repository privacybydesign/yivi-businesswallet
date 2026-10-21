package proofingengine

import (
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// ageOverInfo answers attrAgeOver: whether the holder is Age or older, without
// the date of birth it comes from.
type ageOverInfo struct {
	Age  int  `json:"age"`
	Over bool `json:"over"`
}

// ageOverOf is fd's age answered from the document sess read off the chip, on
// the UTC date of now; nil when fd asks none, or the date of birth did not
// come off the chip. The chip reader refuses a partial date of birth, so one
// that came off the chip parses.
func ageOverOf(sess session.Session, fd *flow.FlowDefinition, now time.Time) *ageOverInfo {
	doc := sess.Steps.Document
	if fd == nil || fd.AgeOver == 0 || doc == nil || doc.Source != session.DocumentSourceChip {
		return nil
	}
	born, err := time.Parse(time.DateOnly, doc.Parsed.DateOfBirth)
	if err != nil {
		return nil
	}
	return &ageOverInfo{Age: fd.AgeOver, Over: reachedAge(born, fd.AgeOver, now)}
}

// reachedAge reports whether someone born on born is age or older on the UTC
// date of now. Born on 29 February, they reach it on 1 March of a common year.
func reachedAge(born time.Time, age int, now time.Time) bool {
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return !today.Before(born.AddDate(age, 0, 0))
}
