package proofingprovider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	headerAdminKey = "X-Admin-Key"
	headerAPIKey   = "X-Api-Key"

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

// CreateTenant creates the IPS tenant id (the org's own id) named name. An id
// IPS already has is ErrTenantExists.
func (c *Client) CreateTenant(ctx context.Context, id, name string) (Tenant, error) {
	var out struct {
		ID            string `json:"id"`
		WebhookSecret string `json:"webhookSecret"`
	}
	body := map[string]any{"id": id, "name": name}
	err := c.do(ctx, http.MethodPost, "/admin/tenants", c.adminHeaders(), body, &out)
	if rejected := (*RejectedError)(nil); errors.As(err, &rejected) && rejected.Status == http.StatusConflict {
		err = ErrTenantExists
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("proofingprovider: create tenant: %w", err)
	}
	if out.ID != id {
		return Tenant{}, errors.New("proofingprovider: create tenant: answer carries another tenant id")
	}
	return Tenant{ID: out.ID, WebhookSecret: out.WebhookSecret}, nil
}

// RotateWebhookSecret issues tenantID a fresh webhook secret and returns it:
// how a tenant whose create answer was lost gets one the wallet holds.
func (c *Client) RotateWebhookSecret(ctx context.Context, tenantID string) (string, error) {
	var out struct {
		WebhookSecret string `json:"webhookSecret"`
	}
	path := "/admin/tenants/" + url.PathEscape(tenantID) + "/webhook-secret"
	if err := c.do(ctx, http.MethodPost, path, c.adminHeaders(), map[string]any{}, &out); err != nil {
		return "", fmt.Errorf("proofingprovider: rotate webhook secret: %w", err)
	}
	if out.WebhookSecret == "" {
		return "", errors.New("proofingprovider: rotate webhook secret: answer carries no secret")
	}
	return out.WebhookSecret, nil
}

// CreateAPIKey mints an env API key for tenantID with scopes and returns its
// plaintext, which IPS shows only this once.
func (c *Client) CreateAPIKey(ctx context.Context, tenantID string, env KeyEnvironment, scopes []string) (string, error) {
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	body := map[string]any{"environment": env, "scopes": scopes}
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
	DeepLink    string    `json:"deepLink"`
	ExpiresAt   time.Time `json:"expiresAt"`
	SlotClaimed bool      `json:"slotClaimed"`
}

func (h *handoverView) claim() *Claim {
	if h == nil || h.DeepLink == "" {
		return nil
	}
	return &Claim{DeepLink: h.DeepLink, ExpiresAt: h.ExpiresAt, Handover: h.SlotClaimed}
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
	if in.CallbackURL != "" {
		body["callbackUrl"], body["callbackPayload"] = in.CallbackURL, "minimal"
	}
	if in.ScriptedOutcome != "" {
		body["scriptedOutcome"] = in.ScriptedOutcome
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

// createdSessionView is IPS's answer to creating a session.
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
			Photo  *ipsImage `json:"photo"`
			Selfie *ipsImage `json:"selfie"`
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

// SessionIdentity reads a session's result for a customer's result read: the
// Result plus the Identity's name, birth date, nationality and Evidence. IPS
// audits it as a personal-data read.
func (c *Client) SessionIdentity(ctx context.Context, apiKey, sessionID, sessionToken string) (Identity, error) {
	var out struct {
		Status      Status     `json:"status"`
		ErrorCode   string     `json:"errorCode"`
		CompletedAt *time.Time `json:"completedAt"`
		Result      *struct {
			Assurance *struct {
				Level      string `json:"level"`
				EIDASLevel string `json:"eidasLevel"`
			} `json:"assurance"`
			Document *struct {
				Type         string `json:"type"`
				IssuingState string `json:"issuingState"`
				Nationality  string `json:"nationality"`
				FirstName    string `json:"firstName"`
				LastName     string `json:"lastName"`
				DateOfBirth  string `json:"dateOfBirth"`
				DateOfExpiry string `json:"dateOfExpiry"`
			} `json:"document"`
			ChipChecks *struct {
				Passive *struct {
					SODSignatureValid    *bool `json:"sodSignatureValid"`
					DataGroupHashesValid *bool `json:"dataGroupHashesValid"`
					CSCATrustChainValid  *bool `json:"cscaTrustChainValid"`
				} `json:"passiveAuthentication"`
				Active *struct {
					Attempted *bool `json:"attempted"`
					Passed    *bool `json:"passed"`
				} `json:"activeAuthentication"`
			} `json:"chipChecks"`
			Biometrics *struct {
				FaceMatchScore *float64 `json:"faceMatchScore"`
				LivenessResult string   `json:"livenessResult"`
			} `json:"biometrics"`
			Disclosure *struct {
				Source string `json:"source"`
			} `json:"disclosure"`
			Photo         *ipsImage `json:"photo"`
			Selfie        *ipsImage `json:"selfie"`
			DocumentImage *ipsImage `json:"documentImage"`
		} `json:"result"`
		Devices []struct {
			Role string `json:"role"`
		} `json:"devices"`
	}
	path := sessionPath(sessionID) + "/result"
	if err := c.do(ctx, http.MethodGet, path, sessionHeaders(apiKey, sessionToken), nil, &out); err != nil {
		return Identity{}, fmt.Errorf("proofingprovider: session identity: %w", err)
	}
	id := Identity{Result: Result{Status: out.Status, ErrorCode: out.ErrorCode, CompletedAt: out.CompletedAt}}
	roles := make([]string, 0, len(out.Devices))
	for _, d := range out.Devices {
		roles = append(roles, d.Role)
	}
	res := out.Result
	id.Method = methodOf(res != nil && res.Disclosure != nil, roles)
	if res == nil {
		return id, nil
	}
	if res.Assurance != nil {
		id.AssuranceLevel, id.EIDASLevel = res.Assurance.Level, res.Assurance.EIDASLevel
	}
	ev := &Evidence{Type: EvidenceEMRTD, PassiveAuth: CheckNotPerformed, ActiveAuth: CheckNotPerformed}
	if res.Disclosure != nil {
		ev.Type = EvidenceYivi
	}
	if doc := res.Document; doc != nil {
		id.GivenName, id.FamilyName = strings.TrimSpace(doc.FirstName), strings.TrimSpace(doc.LastName)
		id.BirthDate, id.Nationality = doc.DateOfBirth, doc.Nationality
		ev.DocumentType, ev.IssuingState, ev.ExpiryDate = doc.Type, doc.IssuingState, doc.DateOfExpiry
	}
	if chip := res.ChipChecks; chip != nil {
		if p := chip.Passive; p != nil {
			ev.PassiveAuth = checkOf(allTrue(p.SODSignatureValid, p.DataGroupHashesValid, p.CSCATrustChainValid))
		}
		if a := chip.Active; a != nil && a.Attempted != nil && *a.Attempted {
			ev.ActiveAuth = checkOf(a.Passed)
		}
	}
	if b := res.Biometrics; b != nil {
		ev.FaceMatch, ev.Liveness = b.FaceMatchScore, b.LivenessResult
	}
	id.Evidence = ev
	id.Photo, id.Selfie, id.DocumentImage = res.Photo.image(), res.Selfie.image(), res.DocumentImage.image()
	return id, nil
}

// ipsImage is an image in an IPS result.
type ipsImage struct {
	ImageBase64 string `json:"imageBase64"`
	MimeType    string `json:"mimeType"`
}

// displayableImageTypes are the formats IPS converts its images to for
// display. Anything else (a JPEG2000 portrait IPS failed to convert, or a type
// a browser would run, like SVG) is left out rather than passed on.
var displayableImageTypes = []string{"image/png", "image/jpeg", "image/webp"}

// image is the Image a result carries, nil when there is none or it is not a
// well-formed image of a displayable type.
func (i *ipsImage) image() *Image {
	if i == nil || i.ImageBase64 == "" || !slices.Contains(displayableImageTypes, i.MimeType) {
		return nil
	}
	if _, err := base64.StdEncoding.DecodeString(i.ImageBase64); err != nil {
		return nil
	}
	return &Image{MimeType: i.MimeType, Base64: i.ImageBase64}
}

// allTrue is nil when a check did not run, else whether each that ran passed.
func allTrue(checks ...*bool) *bool {
	var ran, ok bool
	for _, c := range checks {
		if c == nil {
			continue
		}
		if !ran {
			ran, ok = true, true
		}
		ok = ok && *c
	}
	if !ran {
		return nil
	}
	return &ok
}

func checkOf(passed *bool) string {
	switch {
	case passed == nil:
		return CheckNotPerformed
	case *passed:
		return CheckValid
	default:
		return CheckInvalid
	}
}

// SessionStatus reads a session's status and assurance summary, without the
// document or images and without IPS auditing it as a personal-data read: the
// read to reconcile on. Name is always empty; SessionResult carries it. An IPS
// without the status route (404 while the session exists) is read through
// SessionResult instead, so the two can be deployed in either order.
func (c *Client) SessionStatus(ctx context.Context, apiKey, sessionID, sessionToken string) (Result, error) {
	var out struct {
		Status      Status     `json:"status"`
		ErrorCode   string     `json:"errorCode"`
		CompletedAt *time.Time `json:"completedAt"`
		Assurance   *struct {
			Level      string `json:"level"`
			EIDASLevel string `json:"eidasLevel"`
		} `json:"assurance"`
		Disclosure bool           `json:"disclosure"`
		Devices    []statusDevice `json:"devices"`
	}
	path := sessionPath(sessionID) + "/status"
	err := c.do(ctx, http.MethodGet, path, sessionHeaders(apiKey, sessionToken), nil, &out)
	if errors.Is(err, ErrNotFound) {
		res, err := c.SessionResult(ctx, apiKey, sessionID, sessionToken)
		res.Name = ""
		return res, err
	}
	if err != nil {
		return Result{}, fmt.Errorf("proofingprovider: session status: %w", err)
	}
	res := Result{Status: out.Status, ErrorCode: out.ErrorCode, CompletedAt: out.CompletedAt}
	roles := make([]string, 0, len(out.Devices))
	for _, d := range out.Devices {
		roles = append(roles, d.Role)
	}
	res.Method = methodOf(out.Disclosure, roles)
	res.App = appOf(out.Devices)
	if out.Assurance != nil {
		res.AssuranceLevel, res.EIDASLevel = out.Assurance.Level, out.Assurance.EIDASLevel
	}
	return res, nil
}

// statusDevice is one device of IPS's session status view.
type statusDevice struct {
	Role    string `json:"role"`
	Current bool   `json:"current"`
	Away    bool   `json:"away"`
}

// DecideReview records a reviewer's decision on a session in needs_review; IPS
// then settles it and pushes the outcome. A session not under review is a
// *RejectedError (409).
func (c *Client) DecideReview(ctx context.Context, apiKey, sessionID, sessionToken string, d ReviewDecision) error {
	body := map[string]any{"status": StatusRejected, "reason": d.Reason, "reviewer": d.Reviewer}
	if d.Approve {
		body["status"] = StatusApproved
	} else if d.ErrorCode != "" {
		body["errorCode"] = d.ErrorCode
	}
	path := sessionPath(sessionID) + "/decision"
	if err := c.do(ctx, http.MethodPost, path, sessionHeaders(apiKey, sessionToken), body, nil); err != nil {
		return fmt.Errorf("proofingprovider: decide review: %w", err)
	}
	return nil
}

// CancelSession ends a session that has no outcome yet; one that has is a
// RejectedError (409).
func (c *Client) CancelSession(ctx context.Context, apiKey, sessionID, sessionToken string) error {
	path := sessionPath(sessionID) + "/cancel"
	if err := c.do(ctx, http.MethodPost, path, sessionHeaders(apiKey, sessionToken), map[string]any{}, nil); err != nil {
		return fmt.Errorf("proofingprovider: cancel session: %w", err)
	}
	return nil
}

// DeleteSession erases a session and its data at IPS; one IPS no longer has
// is erased already.
func (c *Client) DeleteSession(ctx context.Context, apiKey, sessionID, sessionToken string) error {
	err := c.do(ctx, http.MethodDelete, sessionPath(sessionID), sessionHeaders(apiKey, sessionToken), nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("proofingprovider: delete session: %w", err)
	}
	return nil
}

// SessionHandover gets a fresh vcmrtd claim link for an Idem session: a new
// claim once the first lapsed unscanned, or a handover when the app holding the
// session left it. An app still active is a RejectedError with CodeDeviceActive.
func (c *Client) SessionHandover(ctx context.Context, apiKey, sessionID, sessionToken string) (Claim, error) {
	var out handoverView
	body := map[string]any{"role": deviceRoleNative}
	path := sessionPath(sessionID) + "/handover"
	if err := c.do(ctx, http.MethodPost, path, sessionHeaders(apiKey, sessionToken), body, &out); err != nil {
		return Claim{}, fmt.Errorf("proofingprovider: session handover: %w", err)
	}
	claim := out.claim()
	if claim == nil {
		return Claim{}, errors.New("proofingprovider: session handover: IPS offered no vcmrtd link")
	}
	return *claim, nil
}

// appPath is a MethodYivi session's subject-facing IPS route. The session token
// in the path is the credential: IPS gives such a session no device slots.
func appPath(sessionToken, route string) string {
	return "/app/" + url.PathEscape(sessionToken) + route
}

// referenceSourceOpenID4VP is how IPS records a reference the wallet verified
// over OpenID4VP (bound_login_reference.go).
const referenceSourceOpenID4VP = "openid4vp"

// SubmitReference hands IPS the photo and identity claims of the subject's
// verified OpenID4VP disclosure as a MethodYivi session's reference, after
// which the face check runs at IPS. IPS answering OK false ended the session.
func (c *Client) SubmitReference(ctx context.Context, apiKey, sessionID, sessionToken string, ref Reference) (YiviDisclosure, error) {
	var out struct {
		OK           bool   `json:"ok"`
		Code         string `json:"code"`
		StableFrames int    `json:"stableFrames"`
		MaxAttempts  int    `json:"maxAttempts"`
	}
	body := map[string]any{
		"source": referenceSourceOpenID4VP, "credential": ref.Credential,
		"image": ref.Photo, "attributes": ref.Attributes,
	}
	path := sessionPath(sessionID) + "/reference"
	if err := c.do(ctx, http.MethodPost, path, sessionHeaders(apiKey, sessionToken), body, &out); err != nil {
		if errors.Is(err, errUnavailable) {
			// IPS takes a wallet's reference only with that option switched on.
			err = ErrMethodUnavailable
		}
		return YiviDisclosure{}, fmt.Errorf("proofingprovider: submit reference: %w", err)
	}
	return YiviDisclosure{OK: out.OK, Code: out.Code, StableFrames: out.StableFrames, MaxAttempts: out.MaxAttempts}, nil
}

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
		msg, code := rejection(limited)
		return &RejectedError{Status: resp.StatusCode, Message: msg, Code: code}
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

// rejection reads IPS's {"error": "...", "code": "..."} body, capped.
func rejection(r io.Reader) (message, code string) {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxErrorMessageBytes)).Decode(&body); err != nil || body.Error == "" {
		return "the identity proofing service rejected the request", body.Code
	}
	return body.Error, body.Code
}
