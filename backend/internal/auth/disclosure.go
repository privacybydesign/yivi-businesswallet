package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

var (
	errSessionNotFinished = errors.New("session not finished")
	errDisclosureInvalid  = errors.New("disclosure invalid")
	errUserNotInvited     = errors.New("user not invited")
)

// extractEmail reads the disclosed email claim. The verifier has already
// verified the presentation cryptographically (signature, key binding, trust
// chain); we only read the disclosed value here.
func extractEmail(res openid4vpverifier.Presentation) (user.Email, error) {
	email, err := user.ParseEmail(res.Claims[openid4vpverifier.ClaimEmail])
	if err != nil {
		return "", errDisclosureInvalid
	}
	return email, nil
}

// extractIdentity reads the identity credentials' claims. It reads them per
// credential (Presentation.IdentityClaims) rather than from the flattened map
// because a combined identity+VOG presentation carries two dateOfBirth claims.
func extractIdentity(res openid4vpverifier.Presentation) (DisclosedIdentity, error) {
	email, err := extractEmail(res)
	if err != nil {
		return DisclosedIdentity{}, err
	}
	claims := res.IdentityClaims()
	given := strings.TrimSpace(claims[openid4vpverifier.ClaimGivenNames])
	family := strings.TrimSpace(claims[openid4vpverifier.ClaimFamilyName])
	if given == "" || family == "" {
		return DisclosedIdentity{}, errDisclosureInvalid
	}
	// Date of birth and phone are disclosed alongside the identity credential;
	// treat them as best-effort (kept when present) rather than a hard requirement
	// of a valid disclosure.
	dateOfBirth := strings.TrimSpace(claims[openid4vpverifier.ClaimDateOfBirth])
	phone := strings.TrimSpace(claims[openid4vpverifier.ClaimPhone])
	return DisclosedIdentity{
		Email:              email,
		Name:               identity.Name{GivenNames: given, LastName: family},
		DateOfBirth:        dateOfBirth,
		Phone:              phone,
		CredentialIssuedAt: res.IdentityIssuedAt,
	}, nil
}

// DisclosedVog is a completed pbdf.vog credential disclosure (#242 §4): the
// same fields a parsed PDF carries (internal/vog.Document), read from the
// credential's own attributes instead. AspectCodes lists only the requested
// codes the credential disclosed as covered - never the full credential, which
// is never requested in the first place.
type DisclosedVog struct {
	GivenNames  string
	Surname     string
	DateOfBirth string
	IssueDate   time.Time
	AspectCodes []string
}

// extractVog reads a pbdf.vog disclosure. requiredAspectCodes is the same list
// the DCQL request named, so exactly those (and no other) aspect claims are
// read back.
func extractVog(res openid4vpverifier.Presentation, requiredAspectCodes []string) (DisclosedVog, error) {
	claims := res.VogClaims()
	given := strings.TrimSpace(claims[openid4vpverifier.ClaimVogGivenNames])
	rawSurname := strings.TrimSpace(claims[openid4vpverifier.ClaimVogSurname])
	surname := rawSurname
	if prefix := strings.TrimSpace(claims[openid4vpverifier.ClaimVogPrefix]); prefix != "" {
		surname = prefix + " " + surname
	}
	dateOfBirth := strings.TrimSpace(claims[openid4vpverifier.ClaimVogDateOfBirth])
	if given == "" || rawSurname == "" || dateOfBirth == "" {
		return DisclosedVog{}, errDisclosureInvalid
	}
	issueDate, err := time.Parse("2006-01-02", strings.TrimSpace(claims[openid4vpverifier.ClaimVogIssueDate]))
	if err != nil {
		return DisclosedVog{}, errDisclosureInvalid
	}

	var codes []string
	for _, code := range requiredAspectCodes {
		if isAffirmative(claims[openid4vpverifier.VogAspectClaim(code)]) {
			codes = append(codes, code)
		}
	}
	return DisclosedVog{GivenNames: given, Surname: surname, DateOfBirth: dateOfBirth, IssueDate: issueDate, AspectCodes: codes}, nil
}

// isAffirmative reads a pbdf.vog aspectNN yes/no claim. Unverified against the
// real credential schema (not yet issued anywhere reachable from this
// environment) - see .ai/features/member-screening-vog.md.
func isAffirmative(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1", "ja":
		return true
	default:
		return false
	}
}

func mapClaimError(err error) error {
	switch {
	case errors.Is(err, errSessionNotFinished):
		return &respond.APIError{
			Status:  http.StatusConflict,
			Code:    "session_not_finished",
			Message: "session not finished",
		}
	case errors.Is(err, errDisclosureInvalid):
		return &respond.APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "disclosure_invalid",
			Message: "disclosure invalid",
		}
	case errors.Is(err, errUserNotInvited):
		return &respond.APIError{
			Status:  http.StatusForbidden,
			Code:    "user_not_invited",
			Message: "you have not been invited",
		}
	default:
		return err
	}
}
