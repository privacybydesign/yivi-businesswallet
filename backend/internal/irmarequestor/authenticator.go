package irmarequestor

import (
	"net/http"

	irma "github.com/privacybydesign/irmago/irma"
)

type RequestAuthenticator interface {
	Authorize(req *irma.DisclosureRequest) (body any, headers http.Header, err error)
}

type tokenAuthenticator struct {
	token string
}

func NewTokenAuthenticator(token string) RequestAuthenticator {
	return tokenAuthenticator{token: token}
}

func (a tokenAuthenticator) Authorize(req *irma.DisclosureRequest) (any, http.Header, error) {
	if a.token == "" {
		return req, nil, nil
	}
	h := http.Header{}
	h.Set("Authorization", a.token)
	return req, h, nil
}
