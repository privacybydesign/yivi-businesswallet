// Package vog implements the parts of the VOG (Verklaring Omtrent het Gedrag)
// screening check that are specific to Justis' document: validating an uploaded
// PDF against validatie.nl in real time (validator.go), reading the fields a
// screening decision needs out of it (parser.go), and the function-aspect code
// list an org's requirement is built from (below). Matching the result against
// a member's identity, checking it against an org's required codes and age
// limit, and everything about storage and audit live in internal/organization,
// which is the consumer of this package. See #242 and
// .ai/features/member-screening-vog.md.
package vog

import (
	"errors"
	"slices"
	"time"
)

// ErrNotAVOG means the document validatie.nl accepted as authentic does not
// carry the fields a VOG has - so parsing found no reason to believe it is one.
var ErrNotAVOG = errors.New("vog: document is not a recognisable VOG")

// ErrUnparseable means a required field (a name or a date the screening
// decision depends on) could not be read from an otherwise-recognised VOG.
var ErrUnparseable = errors.New("vog: a required field could not be read")

// Method is how a screening was performed (member_screenings.method).
type Method string

const (
	// MethodPDF is the primary path: an uploaded PDF, validated live against
	// validatie.nl and parsed.
	MethodPDF Method = "pdf"
	// MethodYiviCredential is the opt-in path: a disclosed pbdf.vog credential.
	MethodYiviCredential Method = "yivi_credential"
)

// Result is the outcome of one screening attempt (member_screenings.result).
type Result string

const (
	ResultValid             Result = "valid"
	ResultRejected          Result = "rejected"
	ResultMismatch          Result = "mismatch"
	ResultInsufficientScope Result = "insufficient_scope"
)

// FunctionAspects are the 19 function-aspect codes a VOG screening profile can
// cover, confirmed empirically against real documents by go-vog-issuer (#242) -
// the published API-specificatie GAAV v1.0 code table is garbled, so this list
// is taken from that project's confirmed set rather than re-derived here.
// Deliberately code-only: the human-readable Dutch/English description per code
// needs sourcing from Justis' own table before it is shown as fact anywhere in
// this product (see .ai/features/member-screening-vog.md).
var FunctionAspects = []string{
	"11", "12", "13", "21", "22", "36", "37", "38", "41", "43",
	"53", "61", "62", "63", "71", "84", "85", "86", "91",
}

// IsFunctionAspect reports whether code is one of the 19 function aspects. A
// code found on a document under "profiel:" that is not a function aspect is a
// specific profile number instead (Document.ProfileCodes) - go-vog-issuer's
// disambiguation rule for the one code (85) that is also a valid-looking
// specific profile number: read as a function aspect first.
func IsFunctionAspect(code string) bool {
	return slices.Contains(FunctionAspects, code)
}

// Document is what a validated VOG carries once parsed from a PDF (parser.go)
// or read out of a disclosed pbdf.vog credential - exactly the fields a
// screening decision needs (identity match, required-code coverage, maximum
// age) and nothing else. Deliberately absent: place of birth, country of birth,
// purpose - #242's data-minimisation design does not need them for a decision
// and a screening record never stores them.
type Document struct {
	GivenNames string
	// Surname includes any name-particle prefix ("van der Berg"), matching
	// identity.Name's own shape so the two compare directly.
	Surname string
	// DateOfBirth is "2006-01-02".
	DateOfBirth string
	IssueDate   time.Time
	// Reference is the kenmerk. The caller hashes it (keyed) before it is ever
	// persisted; Document itself just carries the raw value out of the document.
	Reference string
	// AspectCodes / ProfileCodes are every function-aspect / specific-profile
	// code present on the document, classified via IsFunctionAspect - not yet
	// filtered to what an org requires (see internal/organization for that).
	AspectCodes  []string
	ProfileCodes []string
}

// Codes returns every code the document covers, aspects and specific profiles
// together.
func (d Document) Codes() []string {
	out := make([]string, 0, len(d.AspectCodes)+len(d.ProfileCodes))
	out = append(out, d.AspectCodes...)
	out = append(out, d.ProfileCodes...)
	return out
}
