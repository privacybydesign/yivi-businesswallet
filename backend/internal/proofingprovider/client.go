package proofingprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	headerAdminKey = "X-Admin-Key"
	headerAPIKey   = "X-Api-Key"

	// The IPS API key environment. IPS gives it no runtime meaning today (a
	// sandbox is a tenant flag, not a key kind), so the wallet always mints live.
	keyEnvironment = "live"
	// deviceRoleNative is the vcmrtd app slot; the web slot has no end-user page yet.
	deviceRoleNative = "native"

	// maxResponseBytes caps any IPS answer. A session result embeds the document
	// and face images, which is what sets the size; everything else is small.
	maxResponseBytes = 32 << 20
	// maxErrorMessageBytes caps how much of an IPS rejection message is kept.
	maxErrorMessageBytes = 512
)

// Client drives IPS over HTTP. Admin calls authenticate with the deployment's
// admin key; tenant calls take the org's API key per call, because one wallet
// talks to many IPS tenants.
type Client struct {
	baseURL  string
	adminKey string
	http     *http.Client
}

// NewClient builds an IPS client. baseURL is the IPS origin (the /api/v1 prefix
// is added here).
func NewClient(baseURL, adminKey string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), adminKey: adminKey, http: httpClient}
}

// Ping is the boot-time reachability check against IPS's health probe.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/health", nil, nil, nil)
}

// CreateTenant creates an IPS tenant named name.
func (c *Client) CreateTenant(ctx context.Context, name string) (Tenant, error) {
	var out struct {
		ID            string `json:"id"`
		WebhookSecret string `json:"webhookSecret"`
	}
	body := map[string]any{"name": name}
	if err := c.do(ctx, http.MethodPost, "/admin/tenants", c.adminHeaders(), body, &out); err != nil {
		return Tenant{}, fmt.Errorf("proofingprovider: create tenant: %w", err)
	}
	if out.ID == "" {
		return Tenant{}, errors.New("proofingprovider: create tenant: answer carries no tenant id")
	}
	return Tenant{ID: out.ID, WebhookSecret: out.WebhookSecret}, nil
}

// CreateAPIKey mints an API key for tenantID with scopes and returns its
// plaintext, which IPS shows only this once.
func (c *Client) CreateAPIKey(ctx context.Context, tenantID string, scopes []string) (string, error) {
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	body := map[string]any{"environment": keyEnvironment, "scopes": scopes}
	path := "/admin/tenants/" + url.PathEscape(tenantID) + "/keys"
	if err := c.do(ctx, http.MethodPost, path, c.adminHeaders(), body, &out); err != nil {
		return "", fmt.Errorf("proofingprovider: create api key: %w", err)
	}
	if out.Plaintext == "" {
		return "", errors.New("proofingprovider: create api key: answer carries no key")
	}
	return out.Plaintext, nil
}

// ListFlows returns the active version of each of the tenant's flows.
func (c *Client) ListFlows(ctx context.Context, apiKey string) ([]Flow, error) {
	var out []Flow
	if err := c.do(ctx, http.MethodGet, "/flows", tenantHeaders(apiKey), nil, &out); err != nil {
		return nil, fmt.Errorf("proofingprovider: list flows: %w", err)
	}
	return out, nil
}

// CreateFlow creates a flow (version 1, active).
func (c *Client) CreateFlow(ctx context.Context, apiKey string, in FlowSpec) (Flow, error) {
	var out Flow
	if err := c.do(ctx, http.MethodPost, "/flows", tenantHeaders(apiKey), in, &out); err != nil {
		return Flow{}, fmt.Errorf("proofingprovider: create flow: %w", err)
	}
	return out, nil
}

// CreateFlowVersion adds the next version of flow id; IPS makes it the active one
// at once. Sessions already created keep the version they pinned.
func (c *Client) CreateFlowVersion(ctx context.Context, apiKey, id string, in FlowSpec) (Flow, error) {
	var out Flow
	if err := c.do(ctx, http.MethodPost, flowPath(id)+"/versions", tenantHeaders(apiKey), in, &out); err != nil {
		return Flow{}, fmt.Errorf("proofingprovider: create flow version: %w", err)
	}
	return out, nil
}

// ListFlowVersions returns every version of flow id, oldest first.
func (c *Client) ListFlowVersions(ctx context.Context, apiKey, id string) ([]Flow, error) {
	var out []Flow
	if err := c.do(ctx, http.MethodGet, flowPath(id)+"/versions", tenantHeaders(apiKey), nil, &out); err != nil {
		return nil, fmt.Errorf("proofingprovider: list flow versions: %w", err)
	}
	return out, nil
}

// ActivateFlowVersion makes version the only active version of flow id (a rollback
// or roll-forward); new sessions use it from then on.
func (c *Client) ActivateFlowVersion(ctx context.Context, apiKey, id string, version int) (Flow, error) {
	var out Flow
	path := flowPath(id) + "/versions/" + strconv.Itoa(version) + "/activate"
	if err := c.do(ctx, http.MethodPost, path, tenantHeaders(apiKey), map[string]any{}, &out); err != nil {
		return Flow{}, fmt.Errorf("proofingprovider: activate flow version: %w", err)
	}
	return out, nil
}

func flowPath(id string) string { return "/flows/" + url.PathEscape(id) }

type handoverView struct {
	DeepLink  string    `json:"deepLink"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (h *handoverView) claim() *Claim {
	if h == nil || h.DeepLink == "" {
		return nil
	}
	return &Claim{DeepLink: h.DeepLink, ExpiresAt: h.ExpiresAt}
}

type claimsView struct {
	Native *handoverView `json:"native"`
}

// CreateSession starts a proofing session on a flow.
func (c *Client) CreateSession(ctx context.Context, apiKey string, in SessionInput) (Session, error) {
	body := map[string]any{"flow": in.FlowID, "clientReference": in.ClientReference}
	if in.Language != "" {
		body["language"] = in.Language
	}
	if in.TTL > 0 {
		body["ttlSeconds"] = int(in.TTL / time.Second)
	}
	var out struct {
		ID        string     `json:"id"`
		Token     string     `json:"token"`
		ExpiresAt time.Time  `json:"expiresAt"`
		Claims    claimsView `json:"claims"`
	}
	if err := c.do(ctx, http.MethodPost, "/sessions", tenantHeaders(apiKey), body, &out); err != nil {
		return Session{}, fmt.Errorf("proofingprovider: create session: %w", err)
	}
	if out.ID == "" || out.Token == "" {
		return Session{}, errors.New("proofingprovider: create session: answer carries no session id or token")
	}
	return Session{ID: out.ID, Token: out.Token, ExpiresAt: out.ExpiresAt, Claim: out.Claims.Native.claim()}, nil
}

// MintClaim asks for a fresh vcmrtd claim link. It returns nil when the phone
// slot is already claimed: the subject is past the QR and busy in the app.
func (c *Client) MintClaim(ctx context.Context, apiKey, sessionID, sessionToken string) (*Claim, error) {
	var out claimsView
	path := "/sessions/" + url.PathEscape(sessionID) + "/claim-tokens"
	body := map[string]any{"role": deviceRoleNative}
	if err := c.do(ctx, http.MethodPost, path, sessionHeaders(apiKey, sessionToken), body, &out); err != nil {
		return nil, fmt.Errorf("proofingprovider: mint claim: %w", err)
	}
	return out.Native.claim(), nil
}

// SessionResult reads a session's status and assurance summary.
func (c *Client) SessionResult(ctx context.Context, apiKey, sessionID, sessionToken string) (Result, error) {
	var out struct {
		Status      Status     `json:"status"`
		ErrorCode   string     `json:"errorCode"`
		CompletedAt *time.Time `json:"completedAt"`
		Result      *struct {
			Assurance *struct {
				Level      string `json:"level"`
				EIDASLevel string `json:"eidasLevel"`
			} `json:"assurance"`
		} `json:"result"`
	}
	path := "/sessions/" + url.PathEscape(sessionID) + "/result"
	if err := c.do(ctx, http.MethodGet, path, sessionHeaders(apiKey, sessionToken), nil, &out); err != nil {
		return Result{}, fmt.Errorf("proofingprovider: session result: %w", err)
	}
	res := Result{Status: out.Status, ErrorCode: out.ErrorCode, CompletedAt: out.CompletedAt}
	if out.Result != nil && out.Result.Assurance != nil {
		res.AssuranceLevel = out.Result.Assurance.Level
		res.EIDASLevel = out.Result.Assurance.EIDASLevel
	}
	return res, nil
}

func (c *Client) adminHeaders() http.Header {
	return http.Header{headerAdminKey: {c.adminKey}}
}

func tenantHeaders(apiKey string) http.Header {
	return http.Header{headerAPIKey: {apiKey}}
}

func sessionHeaders(apiKey, sessionToken string) http.Header {
	h := tenantHeaders(apiKey)
	h.Set("Authorization", "Bearer "+sessionToken)
	return h
}

// do makes one IPS call under /api/v1. A nil in sends no body; a nil out discards
// the answer. Errors never carry the URL or a credential (see the package doc).
func (c *Client) do(ctx context.Context, method, path string, headers http.Header, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+path, body)
	if err != nil {
		return errors.New("build request")
	}
	maps.Copy(req.Header, headers)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("unreachable: %w", ctx.Err())
		}
		return errors.New("unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, maxResponseBytes)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusConflict,
		resp.StatusCode == http.StatusUnprocessableEntity:
		return &RejectedError{Status: resp.StatusCode, Message: rejectionMessage(limited)}
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("decode answer (status %d)", resp.StatusCode)
	}
	return nil
}

// rejectionMessage reads IPS's {"error": "..."} body, capped.
func rejectionMessage(r io.Reader) string {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxErrorMessageBytes)).Decode(&body); err != nil || body.Error == "" {
		return "the identity proofing service rejected the request"
	}
	return body.Error
}
