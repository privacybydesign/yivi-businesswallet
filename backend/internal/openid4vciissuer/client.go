package openid4vciissuer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	createOfferPath = "/api/create-offer"
	checkOfferPath  = "/api/check-offer"
	// bodyLimit caps the issuer response we read, guarding against a hostile or
	// broken upstream.
	bodyLimit = 1 << 20

	preAuthGrant = "urn:ietf:params:oauth:grant-type:pre-authorized_code"
	txCodeLength = 6
)

// RequestAuthenticator authorizes a request to the hosted issuer. The Veramo
// issuer expects a Bearer admin token; keeping this an interface makes the auth
// shape config-swappable, the same seam the verifier/QERDS clients use.
type RequestAuthenticator interface {
	Authorize(*http.Request) error
}

// BearerAuthenticator sets the Authorization: Bearer header. An empty token is a
// no-op (dev issuers may be unauthenticated).
type BearerAuthenticator struct{ token string }

func NewBearerAuthenticator(token string) BearerAuthenticator {
	return BearerAuthenticator{token: token}
}

func (b BearerAuthenticator) Authorize(r *http.Request) error {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return nil
}

// VeramoIssuer talks to a hosted Veramo OpenID4VCI issuer over HTTP. The issuer
// is addressed per instance: {baseURL}/{instance}/api/.... pingCredentialID is
// the credential type the boot probe offers to validate URL + token + a
// configured credential.
type VeramoIssuer struct {
	baseURL          string
	instance         string
	auth             RequestAuthenticator
	http             *http.Client
	pingCredentialID string
}

func NewVeramoIssuer(baseURL, instance string, auth RequestAuthenticator, pingCredentialID string, httpClient *http.Client) *VeramoIssuer {
	return &VeramoIssuer{
		baseURL:          strings.TrimRight(baseURL, "/"),
		instance:         instance,
		auth:             auth,
		http:             httpClient,
		pingCredentialID: pingCredentialID,
	}
}

// createOfferRequest is the envelope the Veramo issuer's create-offer expects.
type createOfferRequest struct {
	Credentials                 []string       `json:"credentials"`
	Grants                      map[string]any `json:"grants"`
	CredentialMetadata          map[string]any `json:"credentialMetadata"`
	CredentialDataSupplierInput map[string]any `json:"credentialDataSupplierInput"`
}

type createOfferResponse struct {
	ID     string `json:"id"`
	URI    string `json:"uri"`
	TxCode string `json:"txCode"`
}

// CreateOffer creates a credential offer at the issuer (pre-authorized-code
// grant) and returns the wallet deeplink plus the opaque issuance id to poll.
func (c *VeramoIssuer) CreateOffer(ctx context.Context, req OfferRequest) (Offer, error) {
	preAuth := map[string]any{"pre-authorized_code": "generate"}
	if req.UseTxCode {
		preAuth["tx_code"] = map[string]any{"input_mode": "numeric", "length": txCodeLength}
	}
	claims := req.Claims
	if claims == nil {
		claims = map[string]any{}
	}
	body, err := json.Marshal(createOfferRequest{
		Credentials:                 []string{req.CredentialConfigID},
		Grants:                      map[string]any{preAuthGrant: preAuth},
		CredentialMetadata:          map[string]any{"expiration": req.ExpirationSeconds},
		CredentialDataSupplierInput: claims,
	})
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: marshal create-offer: %w", err)
	}

	httpReq, err := c.newRequest(ctx, createOfferPath, body)
	if err != nil {
		return Offer{}, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: create-offer request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return Offer{}, fmt.Errorf("openid4vciissuer: create-offer returned status %d", resp.StatusCode)
	}

	var out createOfferResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&out); err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: decode create-offer: %w", err)
	}
	if out.ID == "" || out.URI == "" {
		return Offer{}, fmt.Errorf("openid4vciissuer: create-offer response missing id or uri")
	}
	return Offer{IssuanceID: out.ID, OfferURI: out.URI, TxCode: out.TxCode}, nil
}

type checkOfferResponse struct {
	Status string `json:"status"`
}

// Status reports StatusPending until the recipient claims the credential, then
// StatusIssued. A non-2xx or any non-issued status maps to pending.
func (c *VeramoIssuer) Status(ctx context.Context, issuanceID string) (string, error) {
	body, err := json.Marshal(map[string]string{"id": issuanceID})
	if err != nil {
		return "", fmt.Errorf("openid4vciissuer: marshal check-offer: %w", err)
	}
	httpReq, err := c.newRequest(ctx, checkOfferPath, body)
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openid4vciissuer: check-offer request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return StatusPending, nil
	}

	var out checkOfferResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&out); err != nil {
		return "", fmt.Errorf("openid4vciissuer: decode check-offer: %w", err)
	}
	if out.Status != StatusIssued {
		return StatusPending, nil
	}
	return StatusIssued, nil
}

// Ping is the boot readiness probe: it verifies the issuer accepts a create-offer
// of the shape we send (validating URL + admin token + a configured credential).
// Failure is fatal at startup. With no ping credential configured it is a no-op.
func (c *VeramoIssuer) Ping(ctx context.Context) error {
	if c.pingCredentialID == "" {
		return nil
	}
	_, err := c.CreateOffer(ctx, OfferRequest{CredentialConfigID: c.pingCredentialID, ExpirationSeconds: 60})
	return err
}

func (c *VeramoIssuer) newRequest(ctx context.Context, path string, body []byte) (*http.Request, error) {
	url := c.baseURL + "/" + c.instance + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openid4vciissuer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth.Authorize(req); err != nil {
		return nil, fmt.Errorf("openid4vciissuer: authorize request: %w", err)
	}
	return req, nil
}
