package openid4vciissuer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

const (
	eudiOfferPath    = "/credentialOfferReq2"
	eudiMetadataPath = "/.well-known/openid-credential-issuer"
	// eudiIDPrefix marks an issuance id as one of this issuer's, so RulebookRouter
	// can send a status check back to it.
	eudiIDPrefix = "eudi:"
	// eudiEvidenceCountry fills issuing_country when the offer has none: the issuer
	// needs it for its own evidence block, and leaves it out of credentials whose
	// configuration does not list it.
	eudiEvidenceCountry = "NL"
	offerURIScheme      = "openid-credential-offer://?credential_offer="
)

// EudiIssuer talks to the EU reference issuer (eudi-srv-web-issuing-eudiw-py),
// which issues the WE BUILD rulebook credentials under the EUDI profile (HAIP).
// It has no admin API: offers are created over its unauthenticated
// /credentialOfferReq2 endpoint, it keeps no queryable offer state and it runs no
// status list.
type EudiIssuer struct {
	baseURL string
	http    *http.Client
}

func NewEudiIssuer(baseURL string, httpClient *http.Client) *EudiIssuer {
	return &EudiIssuer{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

type eudiOfferPayload struct {
	Credentials []eudiOfferCredential `json:"credentials"`
}

type eudiOfferCredential struct {
	CredentialConfigurationID string         `json:"credential_configuration_id"`
	Data                      map[string]any `json:"data"`
}

type eudiOffer struct {
	CredentialIssuer string `json:"credential_issuer"`
	Grants           map[string]struct {
		PreAuthorizedCode string `json:"pre-authorized_code"`
		IssuerState       string `json:"issuer_state"`
		TxCode            *struct {
			// a number in practice; json.Number also takes a string
			Value json.Number `json:"value"`
		} `json:"tx_code"`
	} `json:"grants"`
}

// CreateOffer creates a pre-authorized-code offer. The issuer always attaches a
// tx_code and puts its value in the offer itself, so the receiving wallet can read
// it there.
func (c *EudiIssuer) CreateOffer(ctx context.Context, req OfferRequest) (Offer, error) {
	data := maps.Clone(req.Claims)
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["issuing_country"]; !ok {
		data["issuing_country"] = eudiEvidenceCountry
	}
	payload, err := json.Marshal(eudiOfferPayload{Credentials: []eudiOfferCredential{
		{CredentialConfigurationID: req.CredentialConfigID, Data: data},
	}})
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: marshal eudi offer: %w", err)
	}
	// The issuer decodes only the payload segment of this unsigned JWT.
	jwt := base64.RawURLEncoding.EncodeToString([]byte("{}")) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
	form := url.Values{"request": {jwt}}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+eudiOfferPath, strings.NewReader(form.Encode()))
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: build eudi offer request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: eudi offer request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return Offer{}, fmt.Errorf("openid4vciissuer: eudi offer returned status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: read eudi offer: %w", err)
	}
	var offer eudiOffer
	if err := json.Unmarshal(raw, &offer); err != nil {
		return Offer{}, fmt.Errorf("openid4vciissuer: decode eudi offer: %w", err)
	}
	grant, ok := offer.Grants[preAuthGrant]
	if !ok || grant.PreAuthorizedCode == "" || offer.CredentialIssuer == "" {
		return Offer{}, fmt.Errorf("openid4vciissuer: eudi offer has no pre-authorized code")
	}
	out := Offer{
		IssuanceID: eudiIDPrefix + grant.IssuerState,
		OfferURI:   offerURIScheme + url.QueryEscape(string(raw)),
	}
	if grant.TxCode != nil {
		out.TxCode = grant.TxCode.Value.String()
	}
	return out, nil
}

// Status is always pending: the issuer cannot tell whether a wallet collected an
// offer.
func (c *EudiIssuer) Status(context.Context, string, string) (IssuanceStatus, error) {
	return IssuanceStatus{Status: StatusPending}, nil
}

// RevokeCredential reports ErrNoStatusListBit: the issuer runs no status list, so
// revocation stays in the local ledger.
func (c *EudiIssuer) RevokeCredential(context.Context, string, string) error {
	return ErrNoStatusListBit
}

// Ping checks the issuer serves its credential issuer metadata.
func (c *EudiIssuer) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+eudiMetadataPath, nil)
	if err != nil {
		return fmt.Errorf("openid4vciissuer: build eudi metadata request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openid4vciissuer: eudi metadata request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openid4vciissuer: eudi metadata returned status %d", resp.StatusCode)
	}
	return nil
}

// Client is an issuer the attestation service can use.
type Client interface {
	Ping(context.Context) error
	CreateOffer(context.Context, OfferRequest) (Offer, error)
	Status(context.Context, string, string) (IssuanceStatus, error)
	RevokeCredential(context.Context, string, string) error
}

// RulebookRouter sends rulebook offers (OfferRequest.Rulebook) to the rulebook
// issuer and everything else to the default one.
type RulebookRouter struct {
	Default   Client
	Rulebooks *EudiIssuer
}

func (r RulebookRouter) Ping(ctx context.Context) error {
	if err := r.Default.Ping(ctx); err != nil {
		return err
	}
	return r.Rulebooks.Ping(ctx)
}

func (r RulebookRouter) CreateOffer(ctx context.Context, req OfferRequest) (Offer, error) {
	if req.Rulebook {
		return r.Rulebooks.CreateOffer(ctx, req)
	}
	return r.Default.CreateOffer(ctx, req)
}

func (r RulebookRouter) Status(ctx context.Context, instance, issuanceID string) (IssuanceStatus, error) {
	if strings.HasPrefix(issuanceID, eudiIDPrefix) {
		return r.Rulebooks.Status(ctx, instance, issuanceID)
	}
	return r.Default.Status(ctx, instance, issuanceID)
}

// RevokeCredential goes to the default issuer: a rulebook credential never gets
// a credential uuid, as its issuer never reports it collected.
func (r RulebookRouter) RevokeCredential(ctx context.Context, instance, credentialUUID string) error {
	return r.Default.RevokeCredential(ctx, instance, credentialUUID)
}
