package signingprovider

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout bounds a single QTSP/OAuth HTTP call.
const DefaultTimeout = 20 * time.Second

// maxBody bounds a response read so a misconfigured endpoint cannot exhaust memory.
const maxBody = 4 << 20 // 4 MiB

// Client talks to a QTSP's CSC API v2 and the OAuth2 authorization server in
// front of it. It is safe for concurrent use.
type Client struct {
	http *http.Client
}

// NewClient returns a Client whose requests are bounded by DefaultTimeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: DefaultTimeout}}
}

// Discover reads the QTSP's unauthenticated /csc/v2/info: the OAuth2 issuer to
// authorize against, plus its name and spec version.
func (c *Client) Discover(ctx context.Context, baseURL string) (Info, error) {
	var out struct {
		Name   string `json:"name"`
		Specs  string `json:"specs"`
		OAuth2 string `json:"oauth2"`
	}
	if err := c.postJSON(ctx, endpoint(baseURL, "/csc/v2/info"), "", map[string]string{"lang": "en-US"}, &out); err != nil {
		return Info{}, err
	}
	return Info{Name: out.Name, Specs: out.Specs, OAuth2: out.OAuth2}, nil
}

// AuthorizeURL builds the /oauth2/authorize URL at the given issuer. PKCE (S256)
// is mandatory at the reference authorization server.
func (c *Client) AuthorizeURL(issuer string, p AuthorizeParams) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	if p.Scope == ScopeCredential {
		q.Set("credentialID", p.CredentialID)
		q.Set("numSignatures", strconv.Itoa(p.NumSignatures))
		q.Set("hashes", strings.Join(p.Hashes, ","))
		q.Set("hashAlgorithmOID", p.HashAlgorithmOID)
	}
	return endpoint(issuer, "/oauth2/authorize") + "?" + q.Encode()
}

// ExchangeToken exchanges an authorization code for an access token using
// client_secret_basic + the PKCE verifier.
func (c *Client) ExchangeToken(ctx context.Context, issuer, clientID, clientSecret, code, codeVerifier, redirectURI string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(issuer, "/oauth2/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, &RequestError{Reason: "could not build the token request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))

	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.do(req, &out); err != nil {
		return Token{}, err
	}
	if out.AccessToken == "" {
		return Token{}, &RequestError{Reason: "the token response carried no access token"}
	}
	return Token{AccessToken: out.AccessToken, TokenType: out.TokenType, ExpiresIn: out.ExpiresIn}, nil
}

// ListCredentials lists the subject's credential ids (the QTSP auto-issues one
// from the presented identity if the subject has none).
func (c *Client) ListCredentials(ctx context.Context, baseURL, accessToken string) ([]string, error) {
	var out struct {
		CredentialIDs []string `json:"credentialIDs"`
	}
	body := map[string]any{"credentialInfo": false, "onlyValid": true}
	if err := c.postJSON(ctx, endpoint(baseURL, "/csc/v2/credentials/list"), accessToken, body, &out); err != nil {
		return nil, err
	}
	return out.CredentialIDs, nil
}

// CredentialInfo resolves a credential's signing certificate + chain and key algo.
func (c *Client) CredentialInfo(ctx context.Context, baseURL, accessToken, credentialID string) (Credential, error) {
	var out struct {
		Key struct {
			Algo []string `json:"algo"`
		} `json:"key"`
		Cert struct {
			Certificates []string `json:"certificates"`
		} `json:"cert"`
	}
	body := map[string]any{"credentialID": credentialID, "certificates": "chain", "certInfo": true}
	if err := c.postJSON(ctx, endpoint(baseURL, "/csc/v2/credentials/info"), accessToken, body, &out); err != nil {
		return Credential{}, err
	}
	if len(out.Cert.Certificates) == 0 {
		return Credential{}, &RequestError{Reason: "the credential carried no certificate"}
	}
	chain, err := parseCertChain(out.Cert.Certificates)
	if err != nil {
		return Credential{}, err
	}
	return Credential{ID: credentialID, Certificate: chain[0], Chain: chain, KeyAlgo: out.Key.Algo}, nil
}

// SignHash asks the QTSP to sign the given base64 raw digests, returning the
// base64 DER signatures (one per hash).
func (c *Client) SignHash(ctx context.Context, baseURL, accessToken, credentialID string, hashesB64 []string, signAlgoOID, hashAlgoOID string) ([]string, error) {
	var out struct {
		Signatures []string `json:"signatures"`
	}
	body := map[string]any{
		"credentialID":     credentialID,
		"hashes":           hashesB64,
		"hashAlgorithmOID": hashAlgoOID,
		"signAlgo":         signAlgoOID,
		"operationMode":    "S",
	}
	if err := c.postJSON(ctx, endpoint(baseURL, "/csc/v2/signatures/signHash"), accessToken, body, &out); err != nil {
		return nil, err
	}
	if len(out.Signatures) == 0 {
		return nil, &RequestError{Reason: "signHash returned no signature"}
	}
	return out.Signatures, nil
}

// postJSON POSTs a JSON body (optionally bearer-authenticated) and decodes the
// JSON response into out.
func (c *Client) postJSON(ctx context.Context, endpoint, bearer string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return &RequestError{Reason: "could not encode the request"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return &RequestError{Reason: "could not build the request"}
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return c.do(req, out)
}

// do executes req and decodes a successful JSON response into out. Every failure
// is mapped to a *RequestError that repeats no URL, credential, or far-side byte.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// net/http embeds the URL in this error; drop it entirely.
		return &RequestError{Reason: "the signing provider could not be reached"}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The status code (read by this side) is safe; the body is the far side's.
		return &RequestError{Reason: fmt.Sprintf("the signing provider returned HTTP %d", resp.StatusCode)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return &RequestError{Reason: "the signing provider returned an unreadable response"}
	}
	return nil
}

// parseCertChain decodes base64-DER certificates (leaf first) into x509.
func parseCertChain(b64 []string) ([]*x509.Certificate, error) {
	chain := make([]*x509.Certificate, 0, len(b64))
	for _, s := range b64 {
		der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, &RequestError{Reason: "the credential certificate was not valid base64"}
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, &RequestError{Reason: "the credential certificate could not be parsed"}
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

// endpoint joins a base URL and a path, tolerating a trailing slash on the base.
func endpoint(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
