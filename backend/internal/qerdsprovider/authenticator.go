package qerdsprovider

import "net/http"

// RequestAuthenticator owns both the request body and headers for a provider
// call. It is a body-level seam (not header-only) for the same reason as
// irmarequestor.RequestAuthenticator: bearer-token auth (plaintext body + a
// header) and signed-JWT auth (the body itself becomes the signed payload) are
// not symmetric — a header-shaped interface fits the first and breaks the
// second. Dev uses the empty-token impl; staging/prod drops in a token or JWT
// impl as config, not a rewrite.
type RequestAuthenticator interface {
	Authorize(body any) (out any, headers http.Header, err error)
}

type tokenAuthenticator struct {
	token string
}

// NewTokenAuthenticator authorizes provider calls with a bearer token. An empty
// token yields no Authorization header (dev / no-auth gateways).
func NewTokenAuthenticator(token string) RequestAuthenticator {
	return tokenAuthenticator{token: token}
}

func (a tokenAuthenticator) Authorize(body any) (any, http.Header, error) {
	if a.token == "" {
		return body, nil, nil
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+a.token)
	return body, h, nil
}
