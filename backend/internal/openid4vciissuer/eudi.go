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
//
// Supported: issuer v0.9.4 up to v0.10.1. From v0.11.0 /credentialOfferReq2 is a
// test feature that wants a signed request and returns the offer and tx_code
// separately, which this client does not do yet.
type EudiIssuer struct {
	baseURL string
	http    *http.Client
	// entries is the status list the issuer reserves its entries in; nil when it
	// issues without one.
	entries StatusEntries
}

// StatusEntries is the Token Status List this deployment keeps for the issuer
// (issuerstatus): the issuer reserves an entry there for each credential it signs.
type StatusEntries interface {
	Taken(ctx context.Context, issuanceID string) (bool, error)
}

// EudiIssuanceID is the issuance id of the offer whose issuer_state is issuerState.
func EudiIssuanceID(issuerState string) string { return eudiIDPrefix + issuerState }

// IsEudiIssuance reports whether issuanceID is an offer of the EU reference
// issuer.
func IsEudiIssuance(issuanceID string) bool { return strings.HasPrefix(issuanceID, eudiIDPrefix) }

func NewEudiIssuer(baseURL string, httpClient *http.Client) *EudiIssuer {
	return &EudiIssuer{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// WithStatusList has the issuer reserve an entry in entries for every credential
// it signs, which makes them revocable.
func (c *EudiIssuer) WithStatusList(entries StatusEntries) *EudiIssuer {
	out := *c
	out.entries = entries
	return &out
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

// Status reports an offer issued once the issuer reserved a status list entry for
// it, which it does while signing the credential a wallet collected. Without a
// status list it cannot tell, and the offer stays pending. The issuance id is the
// credential handle RevokeCredential gets back.
func (c *EudiIssuer) Status(ctx context.Context, _, issuanceID string) (IssuanceStatus, error) {
	if c.entries == nil {
		return IssuanceStatus{Status: StatusPending}, nil
	}
	taken, err := c.entries.Taken(ctx, issuanceID)
	if err != nil {
		return IssuanceStatus{}, err
	}
	if !taken {
		return IssuanceStatus{Status: StatusPending}, nil
	}
	return IssuanceStatus{Status: StatusIssued, CredentialUUID: issuanceID}, nil
}

// RevokeCredential has nothing to call: the credential's status list entry reads
// invalid once the ledger row is revoked. Without a status list it reports
// ErrNoStatusListBit, and revocation stays in the local ledger.
func (c *EudiIssuer) RevokeCredential(context.Context, string, string) error {
	if c.entries == nil {
		return ErrNoStatusListBit
	}
	return nil
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

// RevokeCredential sends a rulebook credential, whose handle is its issuance id,
// to the rulebook issuer, and every other one to the default.
func (r RulebookRouter) RevokeCredential(ctx context.Context, instance, credentialUUID string) error {
	if strings.HasPrefix(credentialUUID, eudiIDPrefix) {
		return r.Rulebooks.RevokeCredential(ctx, instance, credentialUUID)
	}
	return r.Default.RevokeCredential(ctx, instance, credentialUUID)
}

// RulebookStatusList reports whether rulebook credentials get a status list entry.
func (r RulebookRouter) RulebookStatusList() bool { return r.Rulebooks.entries != nil }
