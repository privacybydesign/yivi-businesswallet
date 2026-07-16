package openid4vpverifier

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	presentationsPath = "/ui/presentations"
	nonceBytes        = 16
	// bodyLimit caps the verifier response we read, guarding against a hostile or
	// broken upstream.
	bodyLimit = 1 << 20
)

// Client talks to a hosted OpenID4VP (EUDI) verifier over HTTP. issuerChain pins
// the trusted credential issuer CA (PEM) so the verifier accepts the wallet's
// credentials; it is configuration, never hardcoded.
type Client struct {
	baseURL     string
	issuerChain string
	http        *http.Client
}

func New(baseURL, issuerChain string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), issuerChain: issuerChain, http: httpClient}
}

// startRequest is the envelope the EUDI verifier's /ui/presentations expects.
type startRequest struct {
	Type             string    `json:"type"`
	DCQLQuery        dcqlQuery `json:"dcql_query"`
	Nonce            string    `json:"nonce"`
	JARMode          string    `json:"jar_mode"`
	RequestURIMethod string    `json:"request_uri_method"`
	IssuerChain      string    `json:"issuer_chain,omitempty"`
}

// StartPresentation creates a presentation request at the verifier for the given
// scope and returns the wallet deeplink plus the transaction id to poll. The
// nonce is random per request (never a fixed value).
func (c *Client) StartPresentation(ctx context.Context, scope Scope) (Session, error) {
	nonce, err := randomNonce()
	if err != nil {
		return Session{}, err
	}
	body, err := json.Marshal(startRequest{
		Type:             "vp_token",
		DCQLQuery:        queryFor(scope),
		Nonce:            nonce,
		JARMode:          "by_reference",
		RequestURIMethod: "post",
		IssuerChain:      c.issuerChain,
	})
	if err != nil {
		return Session{}, fmt.Errorf("openid4vpverifier: marshal start request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+presentationsPath, bytes.NewReader(body))
	if err != nil {
		return Session{}, fmt.Errorf("openid4vpverifier: build start request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("openid4vpverifier: start request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return Session{}, fmt.Errorf("openid4vpverifier: start returned status %d", resp.StatusCode)
	}

	var params map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&params); err != nil {
		return Session{}, fmt.Errorf("openid4vpverifier: decode start response: %w", err)
	}
	txID, _ := params["transaction_id"].(string)
	if txID == "" {
		return Session{}, fmt.Errorf("openid4vpverifier: start response missing transaction_id")
	}

	// The remaining params (client_id, request_uri, …) become the wallet request.
	q := url.Values{}
	for k, v := range params {
		if k == "transaction_id" {
			continue
		}
		q.Set(k, fmt.Sprint(v))
	}
	return Session{TransactionID: txID, WalletLink: "openid4vp://?" + q.Encode()}, nil
}

// Result fetches and decodes the disclosed claims for a transaction. It returns
// ErrPending until the holder has completed the presentation.
func (c *Client) Result(ctx context.Context, id string) (Presentation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+presentationsPath+"/"+url.PathEscape(id), nil)
	if err != nil {
		return Presentation{}, fmt.Errorf("openid4vpverifier: build result request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Presentation{}, fmt.Errorf("openid4vpverifier: result request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		// Pending, unknown or expired all surface as non-2xx here.
		return Presentation{}, ErrPending
	}

	var vt vpTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&vt); err != nil {
		return Presentation{}, fmt.Errorf("openid4vpverifier: decode result: %w", err)
	}
	if len(vt.VPToken) == 0 {
		return Presentation{}, ErrPending
	}
	return Presentation{Claims: parseDisclosures(vt.VPToken)}, nil
}

// Status reports StatusPending until the presentation completes, then StatusDone.
func (c *Client) Status(ctx context.Context, id string) (string, error) {
	switch _, err := c.Result(ctx, id); {
	case err == nil:
		return StatusDone, nil
	case errors.Is(err, ErrPending):
		return StatusPending, nil
	default:
		return "", err
	}
}

// Ping is the boot readiness probe: it verifies the verifier will accept a
// presentation request of the shape we send. Failure is fatal at startup.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.StartPresentation(ctx, ScopeLogin)
	return err
}

func randomNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("openid4vpverifier: nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}
