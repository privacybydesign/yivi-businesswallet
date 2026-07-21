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
	createOfferPath      = "/api/create-offer"
	checkOfferPath       = "/api/check-offer"
	revokeCredentialPath = "/api/revoke-credential"
	// bodyLimit caps the issuer response we read, guarding against a hostile or
	// broken upstream.
	bodyLimit = 1 << 20

	preAuthGrant = "urn:ietf:params:oauth:grant-type:pre-authorized_code"
	txCodeLength = 6

	// revokeState is the revoke-credential `state` that sets the status-list bit
	// (any other value would unset it). The issuer replies with one of the
	// revoke*Status values below.
	revokeState = "revoke"

	revokedStatus    = "REVOKED"     // bit was set by this call
	wasRevokedStatus = "WAS_REVOKED" // bit was already set (idempotent)
	unknownStatus    = "UNKNOWN"     // no bit reserved / list not found
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
		Credentials: []string{req.CredentialConfigID},
		Grants:      map[string]any{preAuthGrant: preAuth},
		// enableStatusLists asks the issuer to reserve a Token Status List bit and
		// embed the `status.status_list` reference in the credential, so a later
		// revocation is observable to a verifier. It is a no-op unless the issuer
		// instance has a status list configured (its GitOps `statusLists` block).
		CredentialMetadata:          map[string]any{"expiration": req.ExpirationSeconds, "enableStatusLists": true},
		CredentialDataSupplierInput: claims,
	})
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: marshal create-offer: %w", err)
	}

	httpReq, err := c.newRequest(ctx, req.Instance, createOfferPath, body)
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
	// UUID is the issuer's credential handle, present only once the credential
	// has actually been issued to the wallet. It is what revoke-credential keys
	// on.
	UUID string `json:"uuid"`
}

// Status reports StatusPending until the recipient claims the credential, then
// StatusIssued together with the issuer's credential uuid. A non-2xx or any
// non-issued status maps to pending. instance is the issuer instance the offer
// was created at (empty uses the default).
func (c *VeramoIssuer) Status(ctx context.Context, instance, issuanceID string) (IssuanceStatus, error) {
	body, err := json.Marshal(map[string]string{"id": issuanceID})
	if err != nil {
		return IssuanceStatus{}, fmt.Errorf("openid4vciissuer: marshal check-offer: %w", err)
	}
	httpReq, err := c.newRequest(ctx, instance, checkOfferPath, body)
	if err != nil {
		return IssuanceStatus{}, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return IssuanceStatus{}, fmt.Errorf("openid4vciissuer: check-offer request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return IssuanceStatus{Status: StatusPending}, nil
	}

	var out checkOfferResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&out); err != nil {
		return IssuanceStatus{}, fmt.Errorf("openid4vciissuer: decode check-offer: %w", err)
	}
	if out.Status != StatusIssued {
		return IssuanceStatus{Status: StatusPending}, nil
	}
	return IssuanceStatus{Status: StatusIssued, CredentialUUID: out.UUID}, nil
}

type revokeCredentialRequest struct {
	UUID  string `json:"uuid"`
	State string `json:"state"`
}

type revokeCredentialResponse struct {
	Status string `json:"status"`
}

// RevokeCredential flips the credential's bit on the issuer's Token Status List
// to revoked, so a verifier fetching the signed status list observes the
// revocation. It is idempotent: an already-revoked credential returns success.
// credentialUUID is the issuer's uuid captured at issuance (check-offer's
// `uuid`). instance is the issuer instance that minted the credential.
func (c *VeramoIssuer) RevokeCredential(ctx context.Context, instance, credentialUUID string) error {
	body, err := json.Marshal(revokeCredentialRequest{UUID: credentialUUID, State: revokeState})
	if err != nil {
		return fmt.Errorf("openid4vciissuer: marshal revoke-credential: %w", err)
	}
	httpReq, err := c.newRequest(ctx, instance, revokeCredentialPath, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openid4vciissuer: revoke-credential request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openid4vciissuer: revoke-credential returned status %d", resp.StatusCode)
	}

	var out revokeCredentialResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&out); err != nil {
		return fmt.Errorf("openid4vciissuer: decode revoke-credential: %w", err)
	}
	switch out.Status {
	case revokedStatus, wasRevokedStatus:
		return nil
	case unknownStatus:
		return fmt.Errorf("openid4vciissuer: revoke-credential could not find a status-list bit for the credential")
	default:
		return fmt.Errorf("openid4vciissuer: revoke-credential returned unexpected status %q", out.Status)
	}
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

// newRequest builds a request to an issuer instance. instance selects the
// {instance} path segment; empty falls back to the client's configured default
// instance, so a per-organization instance routes offers to that org's issuer.
func (c *VeramoIssuer) newRequest(ctx context.Context, instance, path string, body []byte) (*http.Request, error) {
	if instance == "" {
		instance = c.instance
	}
	url := c.baseURL + "/" + instance + path
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
