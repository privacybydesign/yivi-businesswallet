package proofing

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The public proofing API: a customer's backend, authenticated by one of the
// customer's API keys (Authorization: Bearer yp_live_…), lists the customer's
// flows, creates sessions for its subjects and reads their outcome. It acts
// exactly as a member sending for the customer would, on the customer's
// assigned flows, and is refused while the customer is paused. A test key
// (yp_test_…) creates test sessions: scripted, in the org's sandbox, unmailed.

type apiKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Mode       Mode       `json:"mode"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

func newAPIKeyResponse(k APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix, Mode: k.Mode, Scopes: k.Scopes,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
	}
}

// createdAPIKeyResponse carries the key's secret, the one time it is readable.
type createdAPIKeyResponse struct {
	apiKeyResponse
	Secret string `json:"secret"`
}

func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	keys, err := h.service.APIKeys(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	out := make([]apiKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, newAPIKeyResponse(k))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
	// Mode is live (the default when empty) or test.
	Mode Mode `json:"mode"`
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	var body createAPIKeyRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	key, secret, err := h.service.CreateAPIKey(r.Context(), orgFromRequest(r).ID, id, caller.ID, body.Name, body.Mode)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, createdAPIKeyResponse{apiKeyResponse: newAPIKeyResponse(key), Secret: secret})
	return nil
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid API key id"}
	}
	key, err := h.service.RevokeAPIKey(r.Context(), orgFromRequest(r).ID, id, keyID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPIKeyResponse(key))
	return nil
}

type apiCallerKey struct{}

func callerFromContext(ctx context.Context) APIKeyCaller {
	c, _ := ctx.Value(apiCallerKey{}).(APIKeyCaller)
	return c
}

const bearerPrefix = "Bearer "

// requireAPIKey authenticates the customer key in the Authorization header.
func (h *Handler) requireAPIKey(scope string, next respond.HandlerFunc) http.Handler {
	return respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="proofing"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "send a customer API key as a Bearer token"}
		}
		caller, err := h.service.AuthenticateAPIKey(r.Context(), strings.TrimSpace(raw))
		if errors.Is(err, ErrAPIKeyInvalid) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="proofing", error="invalid_token"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: "this API key is unknown or revoked"}
		}
		if err != nil {
			return err
		}
		if err := h.service.checkActive(r.Context(), caller.Org.ID); err != nil {
			return mapError(err)
		}
		if !slices.Contains(caller.Scopes, scope) {
			return &respond.APIError{Status: http.StatusForbidden, Code: "insufficient_scope", Message: "this API key lacks the " + scope + " scope"}
		}
		if err := rateLimited(w, h.apiCalls, caller.CustomerID); err != nil {
			return err
		}
		ctx := audit.ContextWithActor(context.WithValue(r.Context(), apiCallerKey{}, caller),
			audit.Actor{Label: APIKeyActorPrefix + caller.KeyPrefix})
		return next(w, r.WithContext(ctx))
	})
}

// limitSessions holds session creation to APISessionLimit per customer, on
// top of APICallLimit: each one costs an IPS session and maybe a mail.
func (h *Handler) limitSessions(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := rateLimited(w, h.apiSessions, callerFromContext(r.Context()).CustomerID); err != nil {
			return err
		}
		return next(w, r)
	}
}

// rateLimited takes a token from the customer's bucket, or answers 429 with
// Retry-After in whole seconds.
func rateLimited(w http.ResponseWriter, limiter *ratelimit.Limiter, customerID uuid.UUID) error {
	ok, wait := limiter.Allow(customerID.String())
	if ok {
		return nil
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	return &respond.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many requests for this customer; retry after the Retry-After seconds"}
}

func (h *Handler) registerPublicAPI(mux *http.ServeMux) {
	mux.Handle("GET /proofing/flows", h.requireAPIKey(ScopeFlowsRead, h.apiListFlows))
	mux.Handle("POST /proofing/sessions", h.requireAPIKey(ScopeSessionsWrite, h.limitSessions(h.idempotent(h.apiCreateSession))))
	mux.Handle("GET /proofing/sessions", h.requireAPIKey(ScopeSessionsRead, h.apiListSessions))
	mux.Handle("GET /proofing/sessions/{sessionID}", h.requireAPIKey(ScopeSessionsRead, h.apiGetSession))
	mux.Handle("GET /proofing/sessions/{sessionID}/result", h.requireAPIKey(ScopeResultsRead, h.apiSessionResult))
	mux.Handle("POST /proofing/sessions/{sessionID}/cancel", h.requireAPIKey(ScopeSessionsWrite, h.idempotent(h.apiCancelSession)))
	mux.Handle("DELETE /proofing/sessions/{sessionID}", h.requireAPIKey(ScopeSessionsWrite, h.apiPurgeSession))
	mux.Handle("POST /proofing/sessions/{sessionID}/methods/{method}", h.requireAPIKey(ScopeSessionsWrite, h.idempotent(h.apiStartMethod)))
	mux.Handle("GET /proofing/sessions/{sessionID}/methods/{method}/status", h.requireAPIKey(ScopeSessionsRead, h.apiMethodStatus))
}

type apiFlowResponse struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Version                int      `json:"version"`
	RequiredAssuranceLevel string   `json:"requiredAssuranceLevel,omitempty"`
	RequestedAttributes    []string `json:"requestedAttributes"`
	Default                bool     `json:"default"`
}

func (h *Handler) apiListFlows(w http.ResponseWriter, r *http.Request) error {
	caller := callerFromContext(r.Context())
	flows, err := h.service.CustomerFlows(r.Context(), caller.Org, caller.CustomerID, false)
	if err != nil {
		return mapError(err)
	}
	out := make([]apiFlowResponse, 0, len(flows))
	for _, f := range flows {
		attrs := f.RequestedAttributes
		if attrs == nil {
			attrs = []string{}
		}
		out = append(out, apiFlowResponse{
			ID: f.ID, Name: f.Name, Version: f.Version, RequiredAssuranceLevel: f.RequiredAssuranceLevel,
			RequestedAttributes: attrs, Default: f.Default,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// apiSessionResponse is a session as the customer's backend sees it: the
// outcome, and for an approved subject the name read off their document until
// the customer's data retention clears it. Never other document data.
type apiSessionResponse struct {
	// ID is the session's ps_ id (PublicSessionID).
	ID             string     `json:"id"`
	Status         Status     `json:"status"`
	FlowID         string     `json:"flowId"`
	FlowName       string     `json:"flowName"`
	FlowVersion    int        `json:"flowVersion,omitempty"`
	Method         string     `json:"method,omitempty"`
	SubjectEmail   string     `json:"subjectEmail"`
	SubjectName    string     `json:"subjectName,omitempty"`
	ProofedName    string     `json:"proofedName,omitempty"`
	AssuranceLevel string     `json:"assuranceLevel,omitempty"`
	EIDASLevel     string     `json:"eidasLevel,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	CancelledAt    *time.Time `json:"cancelledAt,omitempty"`
	// PurgedAt is when the session's data was erased (DELETE).
	PurgedAt *time.Time `json:"purgedAt,omitempty"`
	// Livemode is false for a test key's session.
	Livemode bool `json:"livemode"`
}

func newAPISessionResponse(req Request, now time.Time) apiSessionResponse {
	return apiSessionResponse{
		ID: PublicSessionID(req.ID), Status: req.EffectiveStatus(now), FlowID: req.FlowID, FlowName: req.FlowName,
		FlowVersion: req.FlowVersion, Method: string(req.Method), SubjectEmail: req.SubjectEmail, SubjectName: req.SubjectName,
		ProofedName: req.ProofedName, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel,
		ErrorCode: req.ErrorCode, ExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		CancelledAt: req.CancelledAt, PurgedAt: req.PurgedAt, Livemode: req.mode() == ModeLive,
	}
}

// apiCreatedSessionResponse adds the session's vcmrtd deep link, for a caller
// that shows the QR code itself, and whether the mail went out; a hosted
// session has neither, but the link to its page.
type apiCreatedSessionResponse struct {
	apiSessionResponse
	DeepLink  string `json:"deepLink"`
	MailSent  bool   `json:"mailSent"`
	HostedURL string `json:"hostedUrl,omitempty"`
}

// apiCreateSessionRequest names the subject by address and optional name; an
// absent flowId is the customer's default flow, and sendMail false leaves the
// mail out.
type apiCreateSessionRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	FlowID   string `json:"flowId"`
	SendMail *bool  `json:"sendMail"`
	// Language (en/nl) is the mail's and the Idem app's; empty is the default.
	Language email.Locale `json:"language"`
	// ScriptedOutcome is a test key's outcome: approve (the default), reject:<CODE>,
	// needs_review or expire. A live key may not set it.
	ScriptedOutcome string `json:"scriptedOutcome"`
	// Hosted asks for a link to the hosted page instead of a session: the
	// subject opens it on their own device and starts there; nothing is mailed.
	Hosted bool `json:"hosted"`
	// RedirectURL is where a hosted page sends its subject once the session
	// settles, on one of the customer's allowed redirect origins.
	RedirectURL string `json:"redirectUrl"`
}

func (h *Handler) apiCreateSession(w http.ResponseWriter, r *http.Request) error {
	var body apiCreateSessionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	channel := ChannelEmail
	if body.Hosted {
		channel = ChannelHosted
	}
	sent, err := h.service.CreateRequest(r.Context(), caller.Org,
		Requester{Name: caller.KeyName, APIKeyID: &caller.KeyID}, NewRequest{
			Channel:    channel,
			CustomerID: &caller.CustomerID, SubjectEmail: body.Email, SubjectName: body.Name,
			FlowID: body.FlowID, SkipMail: body.SendMail != nil && !*body.SendMail, Language: body.Language,
			Mode: caller.Mode, ScriptedOutcome: body.ScriptedOutcome, RedirectURL: body.RedirectURL,
		})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, apiCreatedSessionResponse{
		apiSessionResponse: newAPISessionResponse(sent.Request, time.Now()),
		DeepLink:           sent.DeepLink, MailSent: sent.MailSent, HostedURL: sent.HostedURL,
	})
	return nil
}

type apiSessionPage struct {
	Sessions []apiSessionResponse `json:"sessions"`
	// NextCursor fetches the next page (?cursor=); null on the last.
	NextCursor *string `json:"nextCursor"`
}

func (h *Handler) apiListSessions(w http.ResponseWriter, r *http.Request) error {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "limit is a positive number"}
		}
		limit = n
	}
	caller := callerFromContext(r.Context())
	reqs, next, err := h.service.CustomerRequestPage(r.Context(), caller.Org.ID, caller.CustomerID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		return mapError(err)
	}
	now := time.Now()
	out := apiSessionPage{Sessions: make([]apiSessionResponse, 0, len(reqs))}
	for _, req := range reqs {
		out.Sessions = append(out.Sessions, newAPISessionResponse(req, now))
	}
	if next != "" {
		out.NextCursor = &next
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// apiMethodResponse is a headless start: the link the customer's UI shows for
// the picked app, and when the session ends.
type apiMethodResponse struct {
	ID         string    `json:"id"`
	Method     string    `json:"method"`
	AppLink    string    `json:"appLink,omitempty"`
	WalletLink string    `json:"walletLink,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (h *Handler) apiStartMethod(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	method := proofingprovider.Method(r.PathValue("method"))
	caller := callerFromContext(r.Context())
	started, err := h.service.StartHeadless(r.Context(), caller.Org.ID, caller.CustomerID, id, method)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, apiMethodResponse{
		ID: PublicSessionID(started.Request.ID), Method: string(method),
		AppLink: started.AppLink, WalletLink: started.WalletLink, ExpiresAt: started.ExpiresAt,
	})
	return nil
}

// apiMethodStatusResponse is a headless poll, from stored state: done once the
// session has an outcome or ended.
type apiMethodStatusResponse struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Status Status `json:"status"`
	Done   bool   `json:"done"`
}

func (h *Handler) apiMethodStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	req, err := h.service.StoredCustomerRequest(r.Context(), caller.Org.ID, caller.CustomerID, id)
	if err != nil {
		return mapError(err)
	}
	if string(req.Method) != r.PathValue("method") {
		return mapError(ErrWrongMethod)
	}
	status := req.EffectiveStatus(time.Now())
	respond.JSON(w, r, http.StatusOK, apiMethodStatusResponse{
		ID: PublicSessionID(req.ID), Method: string(req.Method), Status: status,
		Done: status != StatusPending && status != StatusInProgress,
	})
	return nil
}

// apiSessionID is the session a customer-API route names.
func apiSessionID(r *http.Request) (uuid.UUID, error) {
	id, ok := parsePublicSessionID(r.PathValue("sessionID"))
	if !ok {
		return uuid.Nil, &respond.APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "this session does not exist"}
	}
	return id, nil
}

// apiResultResponse is a settled session's result: who was proofed, on what
// evidence. identity is set only for an approved session; never the document
// number or an image.
type apiResultResponse struct {
	ID             string             `json:"id"`
	Status         Status             `json:"status"`
	Method         string             `json:"method,omitempty"`
	AssuranceLevel string             `json:"assuranceLevel,omitempty"`
	EIDASLevel     string             `json:"eidasLevel,omitempty"`
	ErrorCode      string             `json:"errorCode,omitempty"`
	VerifiedAt     *time.Time         `json:"verifiedAt,omitempty"`
	Identity       *apiIdentity       `json:"identity,omitempty"`
	Evidence       []apiEvidenceEntry `json:"evidence"`
	Livemode       bool               `json:"livemode"`
}

type apiIdentity struct {
	GivenName   string `json:"givenName,omitempty"`
	FamilyName  string `json:"familyName,omitempty"`
	BirthDate   string `json:"birthDate,omitempty"`
	Nationality string `json:"nationality,omitempty"`
}

type apiEvidenceEntry struct {
	Type         string   `json:"type"`
	DocumentType string   `json:"documentType,omitempty"`
	IssuingState string   `json:"issuingState,omitempty"`
	ExpiryDate   string   `json:"expiryDate,omitempty"`
	PassiveAuth  string   `json:"passiveAuth"`
	ActiveAuth   string   `json:"activeAuth"`
	FaceMatch    *float64 `json:"faceMatch,omitempty"`
	Liveness     string   `json:"liveness,omitempty"`
}

func (h *Handler) apiSessionResult(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	req, identity, err := h.service.RequestResult(r.Context(), caller.Org.ID, caller.CustomerID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPIResultResponse(req, identity, time.Now()))
	return nil
}

// requestResult is an org admin's view of a customer request's identity in the
// wallet, in the customer API's shape plus the face images; each view is
// audited.
func (h *Handler) requestResult(w http.ResponseWriter, r *http.Request) error {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	req, identity, err := h.service.AdminRequestResult(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	out := adminResultResponse{apiResultResponse: newAPIResultResponse(req, identity, time.Now())}
	if identity.Status == proofingprovider.StatusApproved {
		out.Photo, out.Selfie = newAdminImage(identity.Photo), newAdminImage(identity.Selfie)
		out.DocumentImage = newAdminImage(identity.DocumentImage)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// adminResultResponse is the customer API's result as an admin reads it in the
// wallet: for an approval, also the document's portrait, the live selfie
// matched against it and the photo of the document's printed page. The
// customer API never carries an image.
type adminResultResponse struct {
	apiResultResponse
	Photo         *adminImage `json:"photo,omitempty"`
	Selfie        *adminImage `json:"selfie,omitempty"`
	DocumentImage *adminImage `json:"documentImage,omitempty"`
}

// adminImage is a face image, its bytes standard base64.
type adminImage struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func newAdminImage(img *proofingprovider.Image) *adminImage {
	if img == nil {
		return nil
	}
	return &adminImage{MimeType: img.MimeType, Data: img.Base64}
}

// newAPIResultResponse is a settled request's result: the identity only for an
// approval.
func newAPIResultResponse(req Request, identity proofingprovider.Identity, now time.Time) apiResultResponse {
	out := apiResultResponse{
		ID: PublicSessionID(req.ID), Status: req.EffectiveStatus(now), Method: string(identity.Method),
		AssuranceLevel: identity.AssuranceLevel, EIDASLevel: identity.EIDASLevel, ErrorCode: identity.ErrorCode,
		Evidence: []apiEvidenceEntry{}, Livemode: req.mode() == ModeLive,
	}
	if identity.Status == proofingprovider.StatusApproved {
		out.VerifiedAt = identity.CompletedAt
		out.Identity = &apiIdentity{
			GivenName: identity.GivenName, FamilyName: identity.FamilyName,
			BirthDate: identity.BirthDate, Nationality: identity.Nationality,
		}
	}
	if ev := identity.Evidence; ev != nil {
		out.Evidence = append(out.Evidence, apiEvidenceEntry{
			Type: ev.Type, DocumentType: ev.DocumentType, IssuingState: ev.IssuingState, ExpiryDate: ev.ExpiryDate,
			PassiveAuth: ev.PassiveAuth, ActiveAuth: ev.ActiveAuth, FaceMatch: ev.FaceMatch, Liveness: ev.Liveness,
		})
	}
	return out
}

func (h *Handler) apiCancelSession(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	req, err := h.service.CancelRequest(r.Context(), caller.Org.ID, caller.CustomerID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPISessionResponse(req, time.Now()))
	return nil
}

func (h *Handler) apiPurgeSession(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	if err := h.service.PurgeRequest(r.Context(), caller.Org.ID, caller.CustomerID, id); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) apiGetSession(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	req, err := h.service.CustomerRequest(r.Context(), caller.Org.ID, caller.CustomerID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPISessionResponse(req, time.Now()))
	return nil
}
