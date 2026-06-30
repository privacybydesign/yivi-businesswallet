package irmarequestor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"
)

const (
	errBodyLimit          = 4 << 10
	errCodeSessionUnknown = "SESSION_UNKNOWN"
)

var ErrUnknownSession = errors.New("irmarequestor: unknown session")

type Client struct {
	baseURL string
	auth    RequestAuthenticator
	http    *http.Client
}

func New(baseURL string, auth RequestAuthenticator, httpClient *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		auth:    auth,
		http:    httpClient,
	}
}

func (c *Client) StartSession(ctx context.Context, req *irma.DisclosureRequest) (*irmaserver.SessionPackage, error) {
	body, headers, err := c.auth.Authorize(req)
	if err != nil {
		return nil, fmt.Errorf("irmarequestor: authorize: %w", err)
	}

	var pkg irmaserver.SessionPackage
	if err := c.do(ctx, http.MethodPost, "/session", headers, body, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (c *Client) Result(ctx context.Context, token irma.RequestorToken) (*irmaserver.SessionResult, error) {
	var res irmaserver.SessionResult
	path := "/session/" + string(token) + "/result"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error) {
	var res irmaserver.SessionResult
	path := "/session/" + string(token) + "/status"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &res); err != nil {
		return "", err
	}
	return res.Status, nil
}

func (c *Client) CancelSession(ctx context.Context, token irma.RequestorToken) error {
	path := "/session/" + string(token)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// Ping is the one-shot startup readiness gate: it starts (and cancels) each of
// the given disclosure requests. This catches what depends_on:healthy cannot —
// not "is the daemon up" but "will it accept the requests we'll send" (auth
// reaches it, disclose_perms allows the attributes, the scheme resolves), the
// seam that differs between dev irma-demo and prod pbdf. Pass every shape the
// app discloses (login email-only and accept identity) so a perms gap on either
// fails the deploy, not the first user. Failure is fatal; self-heal is the
// orchestrator's restart policy.
func (c *Client) Ping(ctx context.Context, requests ...*irma.DisclosureRequest) error {
	for _, req := range requests {
		pkg, err := c.StartSession(ctx, req)
		if err != nil {
			return fmt.Errorf("irmarequestor: ping: start session: %w", err)
		}
		// Best-effort cleanup: a cancel failure is not a readiness failure, since
		// the start succeeded, which is what Ping asserts.
		_ = c.CancelSession(ctx, pkg.Token)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, headers http.Header, in, out any) error {
	var reqBody io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("irmarequestor: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("irmarequestor: new request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("irmarequestor: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		// The daemon signals an unknown/expired token by its error CODE, not its
		// HTTP status: it answers 400 with {"error":"SESSION_UNKNOWN"}, not 404
		// (validated against the real daemon). Key on the code so the handler can
		// turn it into "start over" regardless of the status irmago chooses.
		var remoteErr irma.RemoteError
		if jsonErr := json.Unmarshal(body, &remoteErr); jsonErr == nil && remoteErr.ErrorName == errCodeSessionUnknown {
			return ErrUnknownSession
		}
		return fmt.Errorf("irmarequestor: %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("irmarequestor: decode response: %w", err)
	}
	return nil
}
