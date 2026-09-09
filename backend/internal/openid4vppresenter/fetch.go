package openid4vppresenter

import (
	"context"
	"fmt"
	"net/http"
)

// requestObjectMediaType is the JAR media type (RFC 9101 §10.2).
const requestObjectMediaType = "application/oauth-authz-req+jwt"

// Fetcher retrieves a Request Object by reference. It always GETs: OpenID4VP 1.0
// §5.10 says a wallet not supporting request_uri_method=post "will send a GET
// request to the Request URI (default behavior)", so post is honored by falling
// back, not by ignoring the parameter — values other than get/post are rejected
// before Fetch is reached (see Service.Start).
type Fetcher struct {
	policy Policy
	client *http.Client
}

func NewFetcher(policy Policy) *Fetcher {
	return &Fetcher{policy: policy, client: newClient(policy)}
}

// Fetch returns the raw Request Object at requestURI, wrapped in
// ErrRequestURIUnreachable on any failure so the handler maps it uniformly and
// the reason stays in the log, not the response.
func (f *Fetcher) Fetch(ctx context.Context, requestURI string) ([]byte, error) {
	u, err := f.policy.checkURL(requestURI)
	if err != nil {
		return nil, fmt.Errorf("%w: request_uri: %w", ErrInvalidRequest, err)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestURIUnreachable, err)
	}
	req.Header.Set("Accept", requestObjectMediaType)
	body, err := do(f.client, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestURIUnreachable, err)
	}
	return body, nil
}
