package csc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// infoPath is the CSC API v2 provider-info endpoint. It is unauthenticated in the
// spec (and in the reference QTSP), so a connection test needs no credential — it
// probes reachability and confirms the endpoint really speaks CSC v2.
const infoPath = "/csc/v2/info"

// DefaultTestTimeout bounds a connection test. It is short: a test is interactive,
// the admin is waiting on it, and a QTSP that cannot answer /info promptly is as
// good as unreachable for the purpose of the test.
const DefaultTestTimeout = 10 * time.Second

// maxInfoBody bounds how much of the /info response is read, so a misconfigured
// URL pointing at something that streams cannot exhaust memory.
const maxInfoBody = 1 << 20 // 1 MiB

// Client probes a CSC provider. It is deliberately tiny: the only call the
// settings slice makes is the unauthenticated /info connection test. The signing
// ceremony (authorize, credentials, signHash) is a separate seam, not built yet.
type Client struct {
	http *http.Client
}

// NewClient returns a Client whose requests are bounded by DefaultTestTimeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: DefaultTestTimeout}}
}

// Info calls POST {baseURL}/csc/v2/info and returns the provider's name and CSC
// spec version. On an unreachable endpoint or a non-2xx answer it returns a
// *TestError whose Reason is safe to show an admin: a status code or a generic
// "unreachable", never the URL and never a byte the far side wrote (a QTSP's
// error document is not a closed set, and it is exactly what an intermediary
// could quote back — so the reason is built from the status code alone).
func (c *Client) Info(ctx context.Context, baseURL string) (Info, error) {
	endpoint := strings.TrimRight(baseURL, "/") + infoPath
	body := bytes.NewReader([]byte(`{"lang":"en-US"}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		// A base URL that cannot form a request was rejected at save time, so this is
		// our own bug rather than the far side's — but it must still not leak the URL.
		return Info{}, &TestError{Reason: "the base URL is not a valid endpoint"}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// net/http puts the URL in this error; the reason must not, so it is dropped
		// entirely rather than quoted.
		return Info{}, &TestError{Reason: "the CSC endpoint could not be reached"}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The status code (a number this side read) is what an admin acts on; the
		// reason phrase and any body are the far side's bytes and are not repeated.
		return Info{}, &TestError{Reason: fmt.Sprintf("the CSC endpoint returned HTTP %d", resp.StatusCode)}
	}

	var info Info
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxInfoBody)).Decode(&info); err != nil {
		return Info{}, &TestError{Reason: "the CSC endpoint did not return a valid /info response"}
	}
	return info, nil
}
