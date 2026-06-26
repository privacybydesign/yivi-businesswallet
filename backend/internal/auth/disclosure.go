package auth

import (
	"errors"
	"net/http"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

var (
	errSessionNotFinished = errors.New("session not finished")
	errDisclosureInvalid  = errors.New("disclosure invalid")
)

func extractEmail(res *irmaserver.SessionResult, want irma.AttributeTypeIdentifier) (string, error) {
	if res.Status != irma.ServerStatusDone {
		return "", errSessionNotFinished
	}
	if res.ProofStatus != irma.ProofStatusValid {
		return "", errDisclosureInvalid
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
	return *attr.RawValue, nil
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
	default:
		return err
	}
}
