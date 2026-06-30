package auth

import (
	"errors"
	"net/http"
	"strings"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

var (
	errSessionNotFinished = errors.New("session not finished")
	errDisclosureInvalid  = errors.New("disclosure invalid")
	errUserNotInvited     = errors.New("user not invited")
)

func disclosureValid(res *irmaserver.SessionResult) error {
	if res.Status != irma.ServerStatusDone {
		return errSessionNotFinished
	}
	if res.ProofStatus != irma.ProofStatusValid {
		return errDisclosureInvalid
	}
	return nil
}

func extractEmail(res *irmaserver.SessionResult, want irma.AttributeTypeIdentifier) (user.Email, error) {
	if err := disclosureValid(res); err != nil {
		return "", err
	}
	if len(res.Disclosed) == 0 || len(res.Disclosed[0]) == 0 {
		return "", errDisclosureInvalid
	}

	attr := res.Disclosed[0][0]
	if attr.Identifier != want || attr.Status != irma.AttributeProofStatusPresent {
		return "", errDisclosureInvalid
	}
	if attr.RawValue == nil || *attr.RawValue == "" {
		return "", errDisclosureInvalid
	}
	email, err := user.ParseEmail(*attr.RawValue)
	if err != nil {
		return "", errDisclosureInvalid
	}
	return email, nil
}

// disclosedValues flattens a multi-attribute disclosure into a lookup of the
// present, non-null raw values by attribute identifier.
func disclosedValues(res *irmaserver.SessionResult) map[irma.AttributeTypeIdentifier]string {
	values := map[irma.AttributeTypeIdentifier]string{}
	for _, con := range res.Disclosed {
		for _, attr := range con {
			if attr.Status == irma.AttributeProofStatusPresent && attr.RawValue != nil {
				values[attr.Identifier] = *attr.RawValue
			}
		}
	}
	return values
}

func extractIdentity(res *irmaserver.SessionResult, attrs IdentityAttributes, emailAttr irma.AttributeTypeIdentifier) (DisclosedIdentity, error) {
	if err := disclosureValid(res); err != nil {
		return DisclosedIdentity{}, err
	}
	values := disclosedValues(res)

	email, err := user.ParseEmail(values[emailAttr])
	if err != nil {
		return DisclosedIdentity{}, errDisclosureInvalid
	}
	given := strings.TrimSpace(values[attrs.GivenNames])
	family := strings.TrimSpace(values[attrs.FamilyName])
	if given == "" || family == "" {
		return DisclosedIdentity{}, errDisclosureInvalid
	}
	return DisclosedIdentity{Email: email, Name: identity.Name{GivenNames: given, LastName: family}}, nil
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
