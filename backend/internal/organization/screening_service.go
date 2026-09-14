package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

// vogValidator is the validatie.nl seam the screening service needs - just the
// operation, not the boot-time Ping (main.go's local interface owns that, the
// same split registryprovider/qerdsprovider use).
type vogValidator interface {
	Validate(ctx context.Context, pdf []byte) (vog.ResponseCode, error)
}

// vogParser is the PDF-reading seam, satisfied by *vog.PDFiumParser (a
// WebAssembly PDFium pool built once at boot) and by a fake in tests.
type vogParser interface {
	Parse(pdf []byte) (vog.Document, error)
}

// vogDiscloser is the pbdf.vog credential-disclosure seam (#242 §4), satisfied
// by *auth.Service - the same split identityDiscloser uses in service.go.
type vogDiscloser interface {
	StartVogSession(ctx context.Context, requiredAspectCodes []string) (auth.Session, error)
	DiscloseVog(ctx context.Context, id string, requiredAspectCodes []string) (auth.DisclosedVog, error)
	StartIdentityVogSession(ctx context.Context, requiredAspectCodes []string) (auth.Session, error)
	DiscloseIdentityAndVog(ctx context.Context, id string, requiredAspectCodes []string) (auth.DisclosedIdentity, auth.DisclosedVog, error)
}

// identityRecorder is the re-identification rule set the combined identity+VOG
// disclosure applies its identity half with (Service.ApplyDisclosedIdentity),
// so an identity established that way is matched, audited and written exactly
// like one from the re-identification link.
type identityRecorder interface {
	ApplyDisclosedIdentity(ctx context.Context, orgID, userID uuid.UUID, email string, storedName identity.Name, disclosed auth.DisclosedIdentity) (identity.Name, error)
}

// screeningStore is the store surface ScreeningService needs, so a test can
// substitute a fake without a real database (mirroring invitationStore /
// Service in service.go). *Store satisfies it structurally.
type screeningStore interface {
	ScreeningMatchContext(ctx context.Context, orgID, userID uuid.UUID) (ScreeningMatchContext, error)
	GetScreeningSettings(ctx context.Context, orgID uuid.UUID) (ScreeningSettings, error)
	RecordScreening(ctx context.Context, orgID, userID uuid.UUID, memberType string, settings ScreeningSettings, in ScreeningInput, hashKey []byte) error
}

// ScreeningOutcome is what one screening attempt produced: the result plus
// enough detail for the caller to explain it, never the disclosed document
// data (#242's data-minimisation design - callers get codes, not names).
type ScreeningOutcome struct {
	Result vog.Result
	// MissingCodes lists the org-required codes the document did not cover;
	// empty unless Result is vog.ResultInsufficientScope.
	MissingCodes []string
	// RejectionReason is a stable, translatable code for a non-valid result -
	// never disclosed document data. One of: "gaav_rejected", "not_a_vog",
	// "unparseable", "identity_mismatch", "too_old", "insufficient_scope".
	RejectionReason string
}

// ScreeningService orchestrates a VOG check end to end (#242 §2, §4): validate
// (PDF path) or read (credential path), match against the member's own stored
// identity, check the org's required codes and maximum age, then hand the
// result to the store to record. A service is warranted (not a plain store
// method, per .ai/conventions/BACKEND.md's "2+ collaborators or cross-domain
// rules" bar): it drives an external validator client plus cross-domain
// matching rules against the member's identity.
type ScreeningService struct {
	store      screeningStore
	validator  vogValidator
	parser     vogParser
	discloser  vogDiscloser
	identities identityRecorder
	hashKey    []byte
}

func NewScreeningService(store *Store, validator vogValidator, parser vogParser, discloser vogDiscloser, identities identityRecorder, hashKey []byte) *ScreeningService {
	return &ScreeningService{store: store, validator: validator, parser: parser, discloser: discloser, identities: identities, hashKey: hashKey}
}

// UploadVog runs the primary PDF path (#242 §2): validate pdf live against
// validatie.nl, parse it, match it against the member's stored identity, and
// check it against the org's required codes and maximum age. checkedBy is
// "self" or "admin"; checkedByUserID is the acting admin's id when it is
// "admin". Returns ErrVogNoDateOfBirth when the membership has no date of birth
// on file yet (the member must re-identify first, #242 §2 step 1) - no
// validatie.nl call is made in that case, and nothing is recorded.
func (s *ScreeningService) UploadVog(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, pdf []byte) (ScreeningOutcome, error) {
	matchCtx, err := s.store.ScreeningMatchContext(ctx, orgID, userID)
	if err != nil {
		return ScreeningOutcome{}, err
	}
	if matchCtx.DateOfBirth == nil {
		return ScreeningOutcome{}, ErrVogNoDateOfBirth
	}

	settings, err := s.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return ScreeningOutcome{}, err
	}

	code, err := s.validator.Validate(ctx, pdf)
	if err != nil {
		return ScreeningOutcome{}, fmt.Errorf("organization: validate vog: %w", err)
	}
	if !code.Authentic() {
		gaavCode := int(code)
		return s.finish(ctx, orgID, userID, matchCtx, settings, ScreeningInput{
			Method: vog.MethodPDF, Result: vog.ResultRejected, CheckedBy: checkedBy, CheckedByUserID: checkedByUserID,
			GAAVResponseCode: &gaavCode,
		}, "gaav_rejected")
	}

	doc, err := s.parser.Parse(pdf)
	gaavCode := int(code)
	if err != nil {
		// Only a verdict about the document is recorded against the member; a
		// parser infrastructure failure (no free PDFium instance, an extraction
		// call failing) is ours and surfaces as an error instead.
		var reason string
		switch {
		case errors.Is(err, vog.ErrNotAVOG):
			reason = "not_a_vog"
		case errors.Is(err, vog.ErrUnparseable):
			reason = "unparseable"
		default:
			return ScreeningOutcome{}, fmt.Errorf("organization: parse vog: %w", err)
		}
		return s.finish(ctx, orgID, userID, matchCtx, settings, ScreeningInput{
			Method: vog.MethodPDF, Result: vog.ResultRejected, CheckedBy: checkedBy, CheckedByUserID: checkedByUserID,
			GAAVResponseCode: &gaavCode,
		}, reason)
	}

	return s.evaluateAndFinish(ctx, orgID, userID, vog.MethodPDF, checkedBy, checkedByUserID, &gaavCode, doc, matchCtx, settings)
}

// requiredAspectCodes returns the org's required codes that are function
// aspects (internal/vog.FunctionAspects) - the only ones the pbdf.vog
// credential can prove, since it carries a yes/no flag per aspect and nothing
// for a specific-profile number. A specific-profile requirement can never be
// satisfied by the credential path; it always ends up in MissingCodes there,
// which is the honest answer given the credential does not carry it (#242 §4).
func requiredAspectCodes(requiredCodes []string) []string {
	var codes []string
	for _, c := range requiredCodes {
		if vog.IsFunctionAspect(c) {
			codes = append(codes, c)
		}
	}
	return codes
}

// StartVogCredentialSession begins the opt-in pbdf.vog disclosure (#242 §4).
// Returns ErrVogCredentialNotAccepted when the org has not opted in
// (ScreeningSettings.AcceptYiviCredential).
func (s *ScreeningService) StartVogCredentialSession(ctx context.Context, orgID uuid.UUID) (auth.Session, error) {
	settings, err := s.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return auth.Session{}, err
	}
	if !settings.AcceptYiviCredential {
		return auth.Session{}, ErrVogCredentialNotAccepted
	}
	return s.discloser.StartVogSession(ctx, requiredAspectCodes(settings.RequiredCodes))
}

// DiscloseVogCredential completes the opt-in pbdf.vog disclosure (#242 §4): read
// the credential's fields, match against the member's stored identity, and
// check them against the org's required codes and maximum age - the same
// decision logic the PDF path uses (evaluateAndFinish), fed a Document built
// from the disclosure instead of a parsed PDF.
func (s *ScreeningService) DiscloseVogCredential(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, disclosureToken string) (ScreeningOutcome, error) {
	matchCtx, err := s.store.ScreeningMatchContext(ctx, orgID, userID)
	if err != nil {
		return ScreeningOutcome{}, err
	}
	if matchCtx.DateOfBirth == nil {
		return ScreeningOutcome{}, ErrVogNoDateOfBirth
	}

	settings, err := s.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return ScreeningOutcome{}, err
	}
	if !settings.AcceptYiviCredential {
		return ScreeningOutcome{}, ErrVogCredentialNotAccepted
	}

	aspectCodes := requiredAspectCodes(settings.RequiredCodes)
	disclosed, err := s.discloser.DiscloseVog(ctx, disclosureToken, aspectCodes)
	if err != nil {
		return ScreeningOutcome{}, fmt.Errorf("organization: disclose vog: %w", err)
	}

	doc := vog.Document{
		GivenNames:  disclosed.GivenNames,
		Surname:     disclosed.Surname,
		DateOfBirth: disclosed.DateOfBirth,
		IssueDate:   disclosed.IssueDate,
		AspectCodes: disclosed.AspectCodes,
	}
	return s.evaluateAndFinish(ctx, orgID, userID, vog.MethodYiviCredential, checkedBy, checkedByUserID, nil, doc, matchCtx, settings)
}

// StartIdentityVogCredentialSession begins the combined identity + pbdf.vog
// disclosure for a member who has never identified: one wallet session
// discloses both, instead of turning the member away with ErrVogNoDateOfBirth
// to identify first. Gated on the org's credential opt-in like
// StartVogCredentialSession.
func (s *ScreeningService) StartIdentityVogCredentialSession(ctx context.Context, orgID uuid.UUID) (auth.Session, error) {
	settings, err := s.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return auth.Session{}, err
	}
	if !settings.AcceptYiviCredential {
		return auth.Session{}, ErrVogCredentialNotAccepted
	}
	return s.discloser.StartIdentityVogSession(ctx, requiredAspectCodes(settings.RequiredCodes))
}

// DiscloseIdentityAndVogCredential completes the combined disclosure. The
// identity half is applied first, under the re-identification rules
// (identityRecorder: email match, name reconciliation, credential freshness)
// and written to the membership; the VOG half is then evaluated against the
// identity just established, so the match is against a verified credential
// and never against the VOG's own say-so. A rejected identity half records no
// screening at all - there is nothing trustworthy to match the VOG against.
func (s *ScreeningService) DiscloseIdentityAndVogCredential(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, disclosureToken string) (ScreeningOutcome, error) {
	matchCtx, err := s.store.ScreeningMatchContext(ctx, orgID, userID)
	if err != nil {
		return ScreeningOutcome{}, err
	}

	settings, err := s.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return ScreeningOutcome{}, err
	}
	if !settings.AcceptYiviCredential {
		return ScreeningOutcome{}, ErrVogCredentialNotAccepted
	}

	aspectCodes := requiredAspectCodes(settings.RequiredCodes)
	disclosedIdentity, disclosed, err := s.discloser.DiscloseIdentityAndVog(ctx, disclosureToken, aspectCodes)
	if err != nil {
		return ScreeningOutcome{}, fmt.Errorf("organization: disclose identity and vog: %w", err)
	}

	name, err := s.identities.ApplyDisclosedIdentity(ctx, orgID, userID, matchCtx.Email, matchCtx.Name, disclosedIdentity)
	if err != nil {
		return ScreeningOutcome{}, err
	}
	dob := parseDateOfBirth(disclosedIdentity.DateOfBirth)
	if dob == nil {
		// The identity credential carried no birth date: the identity is on
		// file now, but a VOG still has nothing to be matched against.
		return ScreeningOutcome{}, ErrVogNoDateOfBirth
	}
	matchCtx.Name, matchCtx.DateOfBirth = name, dob

	doc := vog.Document{
		GivenNames:  disclosed.GivenNames,
		Surname:     disclosed.Surname,
		DateOfBirth: disclosed.DateOfBirth,
		IssueDate:   disclosed.IssueDate,
		AspectCodes: disclosed.AspectCodes,
	}
	return s.evaluateAndFinish(ctx, orgID, userID, vog.MethodYiviCredential, checkedBy, checkedByUserID, nil, doc, matchCtx, settings)
}

// evaluateAndFinish runs the shared decision logic (#242 §2 steps 5-7, common
// to both the PDF and credential paths once a Document is in hand): identity
// match, required-code coverage, maximum age.
func (s *ScreeningService) evaluateAndFinish(ctx context.Context, orgID, userID uuid.UUID, method vog.Method, checkedBy string, checkedByUserID *uuid.UUID, gaavCode *int, doc vog.Document, matchCtx ScreeningMatchContext, settings ScreeningSettings) (ScreeningOutcome, error) {
	in := ScreeningInput{
		Method: method, CheckedBy: checkedBy, CheckedByUserID: checkedByUserID,
		VogIssueDate: &doc.IssueDate, Reference: doc.Reference, GAAVResponseCode: gaavCode,
	}

	if !matchesIdentity(doc, matchCtx) {
		in.Result = vog.ResultMismatch
		return s.finish(ctx, orgID, userID, matchCtx, settings, in, "identity_mismatch")
	}

	covered, missing := evaluateCodes(doc.Codes(), settings.RequiredCodes)
	in.CoveredCodes, in.MissingCodes = covered, missing

	if len(missing) > 0 {
		in.Result = vog.ResultInsufficientScope
		return s.finish(ctx, orgID, userID, matchCtx, settings, in, "insufficient_scope")
	}

	if settings.MaxAgeAtUploadDays != nil {
		maxAge := time.Duration(*settings.MaxAgeAtUploadDays) * 24 * time.Hour
		if time.Since(doc.IssueDate) > maxAge {
			in.Result = vog.ResultRejected
			return s.finish(ctx, orgID, userID, matchCtx, settings, in, "too_old")
		}
	}

	in.Result = vog.ResultValid
	return s.finish(ctx, orgID, userID, matchCtx, settings, in, "")
}

// finish records in via the store and returns the caller-facing outcome.
func (s *ScreeningService) finish(ctx context.Context, orgID, userID uuid.UUID, matchCtx ScreeningMatchContext, settings ScreeningSettings, in ScreeningInput, rejectionReason string) (ScreeningOutcome, error) {
	if err := s.store.RecordScreening(ctx, orgID, userID, matchCtx.MemberType, settings, in, s.hashKey); err != nil {
		return ScreeningOutcome{}, err
	}
	return ScreeningOutcome{Result: in.Result, MissingCodes: in.MissingCodes, RejectionReason: rejectionReason}, nil
}

// matchesIdentity compares a parsed/disclosed VOG's name and date of birth
// against the member's stored identity (#242 §2 step 5), using the same
// case/diacritic-folding key every other identity comparison in this backend
// uses (identity.Name.Key()).
func matchesIdentity(doc vog.Document, matchCtx ScreeningMatchContext) bool {
	docName := identity.Name{GivenNames: doc.GivenNames, LastName: doc.Surname}
	if docName.Key() != matchCtx.Name.Key() {
		return false
	}
	return matchCtx.DateOfBirth != nil && doc.DateOfBirth == matchCtx.DateOfBirth.Format("2006-01-02")
}

// evaluateCodes returns, of the org's required codes, which are present on the
// document (covered) and which are not (missing) - only ever the required
// subset, never the document's full code list (#242's data-minimisation
// design).
func evaluateCodes(documentCodes, requiredCodes []string) (covered, missing []string) {
	present := make(map[string]bool, len(documentCodes))
	for _, c := range documentCodes {
		present[c] = true
	}
	for _, c := range requiredCodes {
		if present[c] {
			covered = append(covered, c)
		} else {
			missing = append(missing, c)
		}
	}
	return covered, missing
}
