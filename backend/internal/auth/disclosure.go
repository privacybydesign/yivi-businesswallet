package auth

import (
	"errors"
	"net/http"
	"strings"

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

func extractIdentity(res openid4vpverifier.Presentation) (DisclosedIdentity, error) {
	email, err := extractEmail(res)
	if err != nil {
		return DisclosedIdentity{}, err
	}
	given := strings.TrimSpace(res.Claims[openid4vpverifier.ClaimGivenNames])
	family := strings.TrimSpace(res.Claims[openid4vpverifier.ClaimFamilyName])
	if given == "" || family == "" {
		return DisclosedIdentity{}, errDisclosureInvalid
	}
	// Phone is disclosed alongside the identity credential; treat it as best-effort
	// (kept when present) rather than a hard requirement of a valid disclosure.
	phone := strings.TrimSpace(res.Claims[openid4vpverifier.ClaimPhone])
	return DisclosedIdentity{
		Email: email,
		Name:  identity.Name{GivenNames: given, LastName: family},
		Phone: phone,
	}, nil
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
