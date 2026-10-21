package proofing

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxLegalRetentionMonths is the longest legal retention an org can set:
	// 50 years.
	maxLegalRetentionMonths = 600
	// maxLegalReasonLength bounds the reason in each language; the person
	// reads it on the page that declines their request.
	maxLegalReasonLength = 500
	// maxDaysPerMonth is longer than any calendar month, so a duration that
	// stands for a legal period never falls short of it.
	maxDaysPerMonth = 31
)

// LegalRetention is how long the org must keep the data of every identity
// check by law, counted from the end of the check, and why. It is the least any
// flow keeps that data, and no erasure takes a session before it has passed.
// The zero value is no legal period. The reasons are shown to the person whose
// "delete my data" request it declines, in their language.
type LegalRetention struct {
	Months   int
	ReasonEN string
	ReasonNL string
}

func (l LegalRetention) set() bool { return l.Months > 0 }

// check reports a legal retention an org may not save; the zero value, which
// removes it, is valid.
func (l LegalRetention) check() error {
	if l == (LegalRetention{}) {
		return nil
	}
	if l.Months < 1 || l.Months > maxLegalRetentionMonths {
		return fmt.Errorf("%w: the legal retention must be 1 to %d months", ErrInvalidInput, maxLegalRetentionMonths)
	}
	for _, reason := range []string{l.ReasonEN, l.ReasonNL} {
		reason = strings.TrimSpace(reason)
		if reason == "" || utf8.RuneCountInString(reason) > maxLegalReasonLength {
			return fmt.Errorf("%w: give the legal retention a reason in English and in Dutch, each of at most %d characters",
				ErrInvalidInput, maxLegalReasonLength)
		}
	}
	return nil
}

// tidy trims the reasons.
func (l LegalRetention) tidy() LegalRetention {
	l.ReasonEN, l.ReasonNL = strings.TrimSpace(l.ReasonEN), strings.TrimSpace(l.ReasonNL)
	return l
}

// heldUntil is when a check that ended at end may be erased: the legal
// retention after it, as the database counts it (purge_at, setPurgeAt).
func (l LegalRetention) heldUntil(end time.Time) time.Time {
	return addMonths(end.UTC(), l.Months)
}

// lasts is how long the legal period runs from now, in the calendar's days.
func (l LegalRetention) lasts(now time.Time) time.Duration {
	if !l.set() {
		return 0
	}
	return l.heldUntil(now).Sub(now.UTC())
}

// longest is the longest the legal period can last, for what keeps data for a
// duration (the engine's session, a proofed name).
func (l LegalRetention) longest() time.Duration {
	return time.Duration(l.Months) * maxDaysPerMonth * hoursPerDay * time.Hour
}

func (l LegalRetention) auditFields() map[string]any {
	var months any
	if l.set() {
		months = l.Months
	}
	return map[string]any{
		"legalRetentionMonths":   months,
		"legalRetentionReasonEn": l.ReasonEN,
		"legalRetentionReasonNl": l.ReasonNL,
	}
}

// addMonths is t plus months as PostgreSQL adds an interval of months: the
// same day of the month, or the last one of a shorter month.
func addMonths(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	lastDay := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(t.Day(), lastDay)-1)
}

// LegalNotice is what a person is told when the legal retention declined their
// erasure: why, in both languages, and when the last of their data can be erased.
type LegalNotice struct {
	Until    time.Time
	ReasonEN string
	ReasonNL string
}
