package openid4vppresenter

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

const (
	// bodyLimit caps what is read from a verifier — the Request Object or the
	// direct_post acknowledgement. The outbound client's 1 MiB cap is the
	// precedent; a JAR is a few KiB.
	bodyLimit = 1 << 20
)

var (
	errBodyTooLarge    = errors.New("response body exceeds limit")
	errUnexpectedState = errors.New("unexpected status")
)

// Policy is the trust posture for every URL a verifier supplies (request_uri,
// response_uri): see safehttp.Policy. AllowInsecureHTTP is the analogue of
// ATTESTATION_HOLDER_ALLOW_INSECURE_HTTP.
type Policy = safehttp.Policy

// readBody reads at most bodyLimit bytes and fails if the body is larger, rather
// than silently truncating a Request Object into something that still parses.
func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, bodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > bodyLimit {
		return nil, errBodyTooLarge
	}
	return b, nil
}

// do performs one guarded request and returns the response body of a 2xx. A
// transport error is stripped of its URL: net/http's *url.Error carries the full
// target, and the request_uri / response_uri must never reach a log line.
func do(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w %d", errUnexpectedState, resp.StatusCode)
	}
	return readBody(resp.Body)
}
