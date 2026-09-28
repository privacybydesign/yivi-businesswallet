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
	method, err := ipsMethod(in.Method)
	if err != nil {
		return Session{}, err
	}
	body := map[string]any{"flow": in.FlowID, "clientReference": in.ClientReference, "method": method}
	if in.Language != "" {
		body["language"] = in.Language
	}
	if in.TTL > 0 {
		body["ttlSeconds"] = int(in.TTL / time.Second)
	}
	var out createdSessionView
	if err := c.do(ctx, http.MethodPost, "/sessions", tenantHeaders(apiKey), body, &out); err != nil {
		if errors.Is(err, errUnavailable) && in.Method == MethodYivi {
			// IPS runs the Yivi method only with a Yivi server configured.
			err = ErrMethodUnavailable
		}
		return Session{}, fmt.Errorf("proofingprovider: create session: %w", err)
	}
	sess, err := out.session()
	if err != nil {
		return Session{}, fmt.Errorf("proofingprovider: create session: %w", err)
	}
	return sess, nil
}

// createdSessionView is IPS's answer to creating and to restarting a session.
type createdSessionView struct {
	ID          string     `json:"id"`
	Token       string     `json:"token"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	FlowVersion int        `json:"flowVersion"`
	Claims      claimsView `json:"claims"`
}

func (v createdSessionView) session() (Session, error) {
	if v.ID == "" || v.Token == "" {
		return Session{}, errors.New("answer carries no session id or token")
	}
	return Session{
		ID: v.ID, Token: v.Token, ExpiresAt: v.ExpiresAt, FlowVersion: v.FlowVersion,
		Claim: v.Claims.Native.claim(),
	}, nil
}

func sessionPath(id string) string { return "/sessions/" + url.PathEscape(id) }

// SessionResult reads a session's status, assurance summary and the holder's name.
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
			// Only the name fields of the document are decoded; the number, the
			// date of birth and the rest are left in the body unread.
			Document *struct {
				DisplayName string `json:"displayName"`
				FirstName   string `json:"firstName"`
				LastName    string `json:"lastName"`
			} `json:"document"`
			// Only whether a Yivi disclosure happened; its attributes stay unread.
			Disclosure *struct {
				Source string `json:"source"`
			} `json:"disclosure"`
		} `json:"result"`
		// Only each device's role: which app took part, not which device.
		Devices []struct {
			Role string `json:"role"`
		} `json:"devices"`
	}
	path := sessionPath(sessionID) + "/result"
	if err := c.do(ctx, http.MethodGet, path, sessionHeaders(apiKey, sessionToken), nil, &out); err != nil {
		return Result{}, fmt.Errorf("proofingprovider: session result: %w", err)
	}
	res := Result{Status: out.Status, ErrorCode: out.ErrorCode, CompletedAt: out.CompletedAt}
	roles := make([]string, 0, len(out.Devices))
	for _, d := range out.Devices {
		roles = append(roles, d.Role)
	}
	res.Method = methodOf(out.Result != nil && out.Result.Disclosure != nil, roles)
	if out.Result != nil && out.Result.Assurance != nil {
		res.AssuranceLevel = out.Result.Assurance.Level
		res.EIDASLevel = out.Result.Assurance.EIDASLevel
	}
	if out.Result != nil && out.Result.Document != nil {
		doc := out.Result.Document
		res.Name = strings.TrimSpace(doc.DisplayName)
		if res.Name == "" {
			res.Name = strings.TrimSpace(strings.TrimSpace(doc.FirstName) + " " + strings.TrimSpace(doc.LastName))
		}
	}
	return res, nil
}

// appPath is a MethodYivi session's subject-facing IPS route. The session token
// in the path is the credential: IPS gives such a session no device slots.
func appPath(sessionToken, route string) string {
	return "/app/" + url.PathEscape(sessionToken) + route
}

// StartYiviDisclosure starts (or, after a cancel in the app, restarts) the Yivi
// disclosure of a MethodYivi session, which IPS then reports opened.
func (c *Client) StartYiviDisclosure(ctx context.Context, sessionToken string) (YiviStart, error) {
	var out struct {
		SessionPtr json.RawMessage `json:"sessionPtr"`
		ExpiresAt  time.Time       `json:"expiresAt"`
	}
	if err := c.do(ctx, http.MethodPost, appPath(sessionToken, "/yivi/start"), nil, map[string]any{}, &out); err != nil {
		if errors.Is(err, errUnavailable) {
			err = ErrMethodUnavailable
		}
		return YiviStart{}, fmt.Errorf("proofingprovider: start yivi disclosure: %w", err)
	}
	if len(out.SessionPtr) == 0 {
		return YiviStart{}, errors.New("proofingprovider: start yivi disclosure: answer carries no session pointer")
	}
	return YiviStart{SessionPtr: out.SessionPtr, ExpiresAt: out.ExpiresAt}, nil
}

// YiviDisclosureResult redeems a MethodYivi session's finished disclosure, or
// answers ErrDisclosurePending while the subject has not finished it.
func (c *Client) YiviDisclosureResult(ctx context.Context, sessionToken string) (YiviDisclosure, error) {
	var out struct {
		OK           bool   `json:"ok"`
		Code         string `json:"code"`
		StableFrames int    `json:"stableFrames"`
		MaxAttempts  int    `json:"maxAttempts"`
	}
	err := c.do(ctx, http.MethodGet, appPath(sessionToken, "/yivi/result"), nil, nil, &out)
	if rejected := (*RejectedError)(nil); errors.As(err, &rejected) && rejected.Status == http.StatusConflict &&
		strings.Contains(rejected.Message, ipsDisclosurePendingMessage) {
		return YiviDisclosure{}, ErrDisclosurePending
	}
	if err != nil {
		return YiviDisclosure{}, fmt.Errorf("proofingprovider: yivi disclosure result: %w", err)
	}
	return YiviDisclosure{OK: out.OK, Code: out.Code, StableFrames: out.StableFrames, MaxAttempts: out.MaxAttempts}, nil
}

// ipsDisclosurePendingMessage is the part of IPS's 409 that tells a Yivi
// session still running from one that is over (bound_login.go).
const ipsDisclosurePendingMessage = "not finished yet"

// SubmitFaceFrame scores one live camera frame (a JPEG data URL or base64) of a
// MethodYivi session against the disclosed photo.
func (c *Client) SubmitFaceFrame(ctx context.Context, sessionToken, image string) (FaceVerdict, error) {
	var out struct {
		FaceDetected bool         `json:"faceDetected"`
		Matched      bool         `json:"matched"`
		Consecutive  int          `json:"consecutive"`
		StableFrames int          `json:"stableFrames"`
		Attempts     int          `json:"attempts"`
		MaxAttempts  int          `json:"maxAttempts"`
		Decision     FaceDecision `json:"decision"`
	}
	body := map[string]string{"image": image}
	if err := c.do(ctx, http.MethodPost, appPath(sessionToken, "/face"), nil, body, &out); err != nil {
		return FaceVerdict{}, fmt.Errorf("proofingprovider: face frame: %w", err)
	}
	return FaceVerdict{
		FaceDetected: out.FaceDetected, Matched: out.Matched, Consecutive: out.Consecutive,
		StableFrames: out.StableFrames, Attempts: out.Attempts, MaxAttempts: out.MaxAttempts, Decision: out.Decision,
	}, nil
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
		resp.StatusCode == http.StatusGone, resp.StatusCode == http.StatusUnprocessableEntity:
		return &RejectedError{Status: resp.StatusCode, Message: rejectionMessage(limited)}
	case resp.StatusCode == http.StatusServiceUnavailable:
		return errUnavailable
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

// errUnavailable is IPS answering 503: a method it cannot run, or IPS itself
// unable to serve. The callers that can tell which say so.
var errUnavailable = fmt.Errorf("status %d", http.StatusServiceUnavailable)

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
