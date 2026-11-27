package proofing

import (
	"context"
	"encoding/json"
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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/clientip"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The public proofing API: an org's software, authenticated by one of the
// org's API keys (Authorization: Bearer yp_live_…), lists the org's flows,
// creates sessions for external people and reads their outcome. It acts as an
// admin sending to an external person would, on any flow of the org, and is
// refused while the org's proofing is paused.

type apiKeyResponse struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	Scopes        []string   `json:"scopes"`
	CreatedByName string     `json:"createdByName,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	// Revealable is a key an admin can read again (GET .../secret).
	Revealable bool `json:"revealable"`
}

func newAPIKeyResponse(k APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, CreatedByName: k.CreatedByName,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, Revealable: k.Revealable,
	}
}

// createdAPIKeyResponse carries the key's secret, the one time it is readable.
type createdAPIKeyResponse struct {
	apiKeyResponse
	Secret string `json:"secret"`
}

func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	keys, err := h.service.APIKeys(r.Context(), orgFromRequest(r).ID)
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
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	var body createAPIKeyRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	key, secret, err := h.service.CreateAPIKey(r.Context(), orgFromRequest(r).ID, caller.ID, body.Name)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, createdAPIKeyResponse{apiKeyResponse: newAPIKeyResponse(key), Secret: secret})
	return nil
}

// apiKeySecretResponse is a key itself, as an admin reads it again.
type apiKeySecretResponse struct {
	Secret string `json:"secret"`
}

func (h *Handler) revealAPIKey(w http.ResponseWriter, r *http.Request) error {
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid API key id"}
	}
	secret, err := h.service.APIKeySecret(r.Context(), orgFromRequest(r).ID, keyID)
	if err != nil {
		return mapError(err)
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, apiKeySecretResponse{Secret: secret})
	return nil
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid API key id"}
	}
	key, err := h.service.RevokeAPIKey(r.Context(), orgFromRequest(r).ID, keyID)
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

// RequireAPIKey lets a request through only with one of the org's API keys
// carrying scope, under the limits every API call is held to. It is exported for
// the other API the org's keys open, the signing API (internal/signing), which
// reads the caller back with APICallerFromContext.
func (h *Handler) RequireAPIKey(scope string, next respond.HandlerFunc) http.Handler {
	return h.requireAPIKey(scope, next)
}

// APICallerFromContext returns the API key a request authenticated with; false
// for a request that passed no RequireAPIKey.
func APICallerFromContext(ctx context.Context) (APIKeyCaller, bool) {
	c, ok := ctx.Value(apiCallerKey{}).(APIKeyCaller)
	return c, ok
}

const bearerPrefix = "Bearer "

// requireAPIKey authenticates the key in the Authorization header.
func (h *Handler) requireAPIKey(scope string, next respond.HandlerFunc) http.Handler {
	return respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		caller, err := h.authenticateAPIKey(w, r)
		if err != nil {
			return err
		}

		// Every answer to a key of an org in test mode says so.
		if caller.TestMode {
			w.Header().Set(headerTestMode, "true")
		}

		if err := rateLimited(w, h.apiCalls, caller.Org.ID.String()); err != nil {
			return err
		}

		if err := h.service.TouchAPIKey(r.Context(), caller); err != nil {
			return err
		}

		if !slices.Contains(caller.Scopes, scope) {
			return &respond.APIError{Status: http.StatusForbidden, Code: "insufficient_scope", Message: "this API key lacks the " + scope + " scope"}
		}
		if !caller.WalletActive && r.Method != http.MethodGet && r.Method != http.MethodHead {
			return &respond.APIError{Status: http.StatusConflict, Code: organization.WalletNotActiveCode, Message: "this organization's wallet is suspended or revoked"}
		}

		ctx := audit.ContextWithActor(context.WithValue(r.Context(), apiCallerKey{}, caller),
			audit.Actor{Label: APIKeyActorPrefix + caller.KeyPrefix})
		return next(w, r.WithContext(ctx))
	})
}

// authenticateAPIKey is the caller of the Bearer key r carries, or the 401
// for a missing, unknown or revoked one.
func (h *Handler) authenticateAPIKey(w http.ResponseWriter, r *http.Request) (APIKeyCaller, error) {
	raw, ok := strings.CutPrefix(r.Header.Get(headerAuthorization), bearerPrefix)
	if !ok || raw == "" {
		w.Header().Set(headerWWWAuthenticate, `Bearer realm="proofing"`)
		return APIKeyCaller{}, &respond.APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "send an API key as a Bearer token"}
	}
	caller, err := h.service.AuthenticateAPIKey(r.Context(), strings.TrimSpace(raw))
	if !errors.Is(err, ErrAPIKeyInvalid) {
		return caller, err
	}
	if err := h.failureLimited(w, r); err != nil {
		return APIKeyCaller{}, err
	}
	w.Header().Set(headerWWWAuthenticate, `Bearer realm="proofing", error="invalid_token"`)
	return APIKeyCaller{}, &respond.APIError{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: "this API key is unknown or revoked"}
}

// limitSessions holds session creation to APISessionLimit per org, on
// top of apiCallLimit: each one costs an engine session and maybe a mail.
func (h *Handler) limitSessions(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := rateLimited(w, h.apiSessions, callerFromContext(r.Context()).Org.ID.String()); err != nil {
			return err
		}
		return next(w, r)
	}
}

// rateLimited takes a token from key's bucket, or answers 429 with
// Retry-After in whole seconds.
func rateLimited(w http.ResponseWriter, limiter *ratelimit.Limiter, key string) error {
	ok, wait := limiter.Allow(key)
	if ok {
		return nil
	}
	w.Header().Set(headerRetryAfter, strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	return &respond.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many requests; retry after the Retry-After seconds"}
}

// authFailureLimit is how many unknown API keys or hosted links one source
// may present: past it a guess answers 429 instead of 401 or 404. It never
// holds a valid key or link, so a shared source (a proxy) is never locked out
// by someone else's guesses.
var authFailureLimit = ratelimit.Limit{Burst: 60, Per: time.Minute}

// failureLimited counts a failed key or link against its source.
func (h *Handler) failureLimited(w http.ResponseWriter, r *http.Request) error {
	return rateLimited(w, h.authFailures, requestSource(r))
}

// requestSource is the source a call came from, its client address or that
// address's IPv6 /64: what authFailures and hostedSources key on.
func requestSource(r *http.Request) string {
	return clientip.SourceKey(r)
}

func (h *Handler) registerPublicAPI(mux *http.ServeMux) {
	mux.Handle("GET /proofing/flows", h.requireAPIKey(ScopeFlowsRead, h.apiListFlows))
	mux.Handle("POST /proofing/sessions", h.requireAPIKey(ScopeSessionsWrite, h.limitSessions(h.idempotent(h.apiCreateSession))))
	mux.Handle("GET /proofing/sessions", h.requireAPIKey(ScopeSessionsRead, h.apiListSessions))
	mux.Handle("GET /proofing/sessions/{sessionID}", h.requireAPIKey(ScopeSessionsRead, h.apiGetSession))
	mux.Handle("GET /proofing/sessions/{sessionID}/result", h.requireAPIKey(ScopeResultsRead, h.apiSessionResult))
	mux.Handle("GET /proofing/sessions/{sessionID}/data-export", h.requireAPIKey(ScopeResultsRead, h.apiDataExport))
	mux.Handle("POST /proofing/sessions/{sessionID}/cancel", h.requireAPIKey(ScopeSessionsWrite, h.idempotent(h.apiCancelSession)))
	mux.Handle("DELETE /proofing/sessions/{sessionID}", h.requireAPIKey(ScopeSessionsWrite, h.apiPurgeSession))
	mux.Handle("POST /proofing/sessions/{sessionID}/methods/{method}", h.requireAPIKey(ScopeSessionsWrite, h.idempotent(h.apiStartMethod)))
	mux.Handle("GET /proofing/sessions/{sessionID}/methods/{method}/status", h.requireAPIKey(ScopeSessionsRead, h.apiMethodStatus))
	mux.Handle("POST /proofing/sessions/{sessionID}/document-scan", h.requireAPIKey(ScopeSessionsWrite, h.apiStartDocumentScan))
	mux.Handle("POST /proofing/sessions/{sessionID}/document-scan/finish", h.requireAPIKey(ScopeSessionsWrite, h.apiFinishDocumentScan))
}

type apiFlowResponse struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Version                int      `json:"version"`
	RequiredAssuranceLevel string   `json:"requiredAssuranceLevel,omitempty"`
	RequestedAttributes    []string `json:"requestedAttributes"`
	// AgeOver is the age age_over answers for; absent when the flow does not
	// request it.
	AgeOver int  `json:"ageOver,omitempty"`
	Default bool `json:"default"`
	// flowNeedsReferencePhoto is a flow that matches the face without reading
	// the chip: every session on it carries a referencePhoto.
	NeedsReferencePhoto bool `json:"needsReferencePhoto"`
	// Allowed is the flow being offered to the org's members.
	Allowed bool `json:"allowed"`
	// HostedEnabled is whether the flow is sent as a hosted link, and
	// HostedReturnURL the page its links send the person back to; absent for
	// none.
	HostedEnabled   bool   `json:"hostedEnabled"`
	HostedReturnURL string `json:"hostedReturnUrl,omitempty"`
	// The steps after the identity check the flow asks for.
	DiplomaMode    DiplomaMode    `json:"diplomaMode"`
	AddressMode    AddressMode    `json:"addressMode"`
	EmploymentMode EmploymentMode `json:"employmentMode"`
}

func (h *Handler) apiListFlows(w http.ResponseWriter, r *http.Request) error {
	caller := callerFromContext(r.Context())
	flows, err := h.service.Flows(r.Context(), caller.Org, FlowsAll)
	if err != nil {
		return mapError(err)
	}
	out := make([]apiFlowResponse, 0, len(flows))
	for _, f := range flows {
		// A data request is sent by its flowKind, on the org's standard flow.
		if !externalCompletable(f.Flow) || f.Kind.dataRequest() {
			continue
		}
		attrs := f.RequestedAttributes
		if attrs == nil {
			attrs = []string{}
		}
		out = append(out, apiFlowResponse{
			ID: f.ID, Name: f.Name, Version: f.Version, RequiredAssuranceLevel: f.RequiredAssuranceLevel,
			RequestedAttributes: attrs, AgeOver: f.AgeOver, Default: f.Default, NeedsReferencePhoto: flowNeedsReferencePhoto(f.Flow),
			Allowed: f.Allowed, HostedEnabled: f.HostedEnabled, HostedReturnURL: f.HostedReturnURL,
			DiplomaMode: f.Diplomas, AddressMode: f.Address, EmploymentMode: f.Employment,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// apiSessionResponse is a session as the org's software sees it: the
// outcome, and for an approved subject the name read off their document until
// the org's data retention clears it. Never other document data.
type apiSessionResponse struct {
	// ID is the session's ps_ id (publicSessionID).
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
	// CancelledBy is who cancelled it; absent for one cancelled before the
	// wallet kept it. ExpiryReason is why an expired session expired.
	CancelledBy  CancelledBy  `json:"cancelledBy,omitempty"`
	ExpiryReason ExpiryReason `json:"expiryReason,omitempty"`
	// PurgedAt is when the session's data was erased (DELETE).
	PurgedAt *time.Time `json:"purgedAt,omitempty"`
	// ExpectedSubject is a session for one known person, created with a
	// birthDate: only that name and birth date are approved (IDENTITY_MISMATCH
	// otherwise).
	ExpectedSubject bool `json:"expectedSubject"`
	// Test is a session made while the org was in test mode.
	Test bool `json:"test,omitempty"`
	// FlowKind is what the session is for: identity, or data_access /
	// data_erasure, which goes to the org's review once the person is proven.
	// DataExportUntil is until when an approved data_access session's data
	// downloads (GET .../data-export).
	FlowKind        FlowKind   `json:"flowKind"`
	DataExportUntil *time.Time `json:"dataExportUntil,omitempty"`
	// AddressReview is where an address the registered address did not
	// confirm stands with a reviewer.
	AddressReview AddressReview `json:"addressReview,omitempty"`
}

func newAPISessionResponse(req Request, now time.Time) apiSessionResponse {
	return apiSessionResponse{
		ID: publicSessionID(req.ID), Status: req.EffectiveStatus(now), FlowID: req.FlowID, FlowName: req.FlowName,
		FlowVersion: req.FlowVersion, Method: string(req.Method), SubjectEmail: req.SubjectEmail, SubjectName: req.SubjectName,
		ProofedName: req.ProofedName, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel,
		ErrorCode: req.ErrorCode, ExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		CancelledAt: req.CancelledAt, CancelledBy: req.CancelledBy, ExpiryReason: req.ExpiryReason(now), PurgedAt: req.PurgedAt,
		ExpectedSubject: req.ExpectsSubject, Test: req.Test, FlowKind: req.FlowKind, DataExportUntil: req.DataExportUntil,
		AddressReview: req.AddressReview,
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
// absent flowId is the org's default flow, and sendMail false leaves the
// mail out.
type apiCreateSessionRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	// BirthDate (YYYY-MM-DD) with Name makes the session one for that person
	// only: anyone else is rejected with IDENTITY_MISMATCH.
	BirthDate string `json:"birthDate"`
	// ReferencePhoto is the caller's own photo of the subject's face
	// (standard base64 of a PNG, JPEG or WebP, at most maxReferencePhotoBytes),
	// for a flow that matches the face without the chip read.
	ReferencePhoto string `json:"referencePhoto"`
	FlowID         string `json:"flowId"`
	// FlowKind data_access or data_erasure starts a data request ("see my
	// data", "delete my data") on the org's standard flow of that kind;
	// empty or identity is an identity check on FlowID.
	FlowKind FlowKind `json:"flowKind"`
	SendMail *bool    `json:"sendMail"`
	// Language (en/nl) is the mail's and the Idem app's; empty is the default.
	Language email.Locale `json:"language"`
	// Hosted asks for a link to the hosted page instead of a session: the
	// subject opens it on their own device and starts there; nothing is mailed.
	Hosted bool `json:"hosted"`
	// RedirectURL is where a hosted page sends its subject once the session
	// settles, on one of the org's allowed redirect origins; absent takes the
	// flow's return page.
	RedirectURL string `json:"redirectUrl"`
	// Redirect false ends a hosted page on its own done screen, though its
	// flow has a return page.
	Redirect *bool `json:"redirect"`
}

func (h *Handler) apiCreateSession(w http.ResponseWriter, r *http.Request) error {
	// A reference photo makes this the one large body the API takes.
	r.Body = http.MaxBytesReader(w, r.Body, maxIdempotentBody)
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
			Channel: channel, SubjectEmail: body.Email, SubjectName: body.Name,
			SubjectBirthDate: body.BirthDate, ReferencePhoto: body.ReferencePhoto, FlowID: body.FlowID, FlowKind: body.FlowKind,
			SkipMail: body.SendMail != nil && !*body.SendMail, Language: body.Language,
			RedirectURL: body.RedirectURL, NoRedirect: body.Redirect != nil && !*body.Redirect,
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
	reqs, next, err := h.service.ExternalRequestPage(r.Context(), caller.Org.ID, r.URL.Query().Get("cursor"), limit)
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

// apiMethodResponse is a headless start: the link the caller's UI shows for
// the picked app, and when the session ends.
type apiMethodResponse struct {
	ID         string    `json:"id"`
	Method     string    `json:"method"`
	AppLink    string    `json:"appLink,omitempty"`
	WalletLink string    `json:"walletLink,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// apiStartMethods are the methods an integrator starts a session in. Pending
// is the hosted page's alone: the API has no call to choose the app after it.
var apiStartMethods = []proofingprovider.Method{
	proofingprovider.MethodIdem, proofingprovider.MethodYivi, proofingprovider.MethodBrowser,
}

func (h *Handler) apiStartMethod(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	method := proofingprovider.Method(r.PathValue("method"))
	if !slices.Contains(apiStartMethods, method) {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "method must be idem_app, yivi_app or browser"}
	}
	caller := callerFromContext(r.Context())
	started, err := h.service.StartHeadless(r.Context(), caller.Org.ID, id, method)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, apiMethodResponse{
		ID: publicSessionID(started.Request.ID), Method: string(method),
		AppLink: started.AppLink, WalletLink: started.WalletLink, ExpiresAt: started.ExpiresAt,
	})
	return nil
}

// apiMethodStatusResponse is a headless poll: done once the session has an
// outcome or ended.
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
	req, err := h.service.HeadlessStatus(r.Context(), caller.Org.ID, id, proofingprovider.Method(r.PathValue("method")))
	if err != nil {
		return mapError(err)
	}
	status := req.EffectiveStatus(time.Now())
	respond.JSON(w, r, http.StatusOK, apiMethodStatusResponse{
		ID: publicSessionID(req.ID), Method: string(req.Method), Status: status,
		Done: status != StatusPending && status != StatusInProgress,
	})
	return nil
}

// apiSessionID is the session an API route names.
func apiSessionID(r *http.Request) (uuid.UUID, error) {
	id, ok := parsePublicSessionID(r.PathValue("sessionID"))
	if !ok {
		return uuid.Nil, &respond.APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "this session does not exist"}
	}
	return id, nil
}

// apiResultResponse is a settled session's result: who was proofed, on what
// evidence. identity and the document's images are set only for an approved
// session; never the document number.
type apiResultResponse struct {
	ID             string `json:"id"`
	Status         Status `json:"status"`
	Method         string `json:"method,omitempty"`
	AssuranceLevel string `json:"assuranceLevel,omitempty"`
	EIDASLevel     string `json:"eidasLevel,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	// ReviewReason is why the kept Verzekeringsbericht went to review
	// (no_current_employment, current_benefit, current_other_income); absent
	// otherwise.
	ReviewReason string `json:"reviewReason,omitempty"`
	// Steps are the extra steps the flow asks for, each with its own outcome
	// beside the identity's.
	Steps      []StepOutcome      `json:"steps,omitempty"`
	VerifiedAt *time.Time         `json:"verifiedAt,omitempty"`
	Identity   *apiIdentity       `json:"identity,omitempty"`
	Evidence   []apiEvidenceEntry `json:"evidence"`
	// Diplomas are the DUO diploma extracts the subject added, each checked
	// against DUO's signature and the proofed identity.
	Diplomas []diplomaResponse `json:"diplomas,omitempty"`
	// Address is the postal address the subject entered after their identity
	// was approved; as they declared it, not read off a document.
	Address *Address `json:"address,omitempty"`
	// Employment is what was kept of the subject's UWV Verzekeringsbericht
	// after their identity was approved, checked against UWV's seal and the
	// proofed identity.
	Employment *Employment `json:"employment,omitempty"`
	// AddressReview is where an address the registered address did not
	// confirm stands with a reviewer.
	AddressReview AddressReview `json:"addressReview,omitempty"`
	// Photo is the document's portrait; DocumentImage and DocumentImageBack
	// the photos of its front and back, as far as the engine still holds them.
	Photo             *adminImage `json:"photo,omitempty"`
	DocumentImage     *adminImage `json:"documentImage,omitempty"`
	DocumentImageBack *adminImage `json:"documentImageBack,omitempty"`
	// Selfie is the live face matched against the document, released only
	// when the flow keeps the face photos and requests the selfie.
	Selfie *adminImage `json:"selfie,omitempty"`
}

type apiIdentity struct {
	GivenName   string        `json:"givenName,omitempty"`
	FamilyName  string        `json:"familyName,omitempty"`
	BirthDate   string        `json:"birthDate,omitempty"`
	Nationality asked[string] `json:"nationality,omitzero"`
	// BSN is the holder's BSN, for a flow whose BSN policy keeps or masks it.
	BSN asked[string] `json:"bsn,omitzero"`
	// DutchNationality is the registered personal data's yes/no on whether the
	// holder is Dutch.
	DutchNationality asked[bool] `json:"dutchNationality,omitzero"`
	// AgeOver is set for a flow that requests age_over, which releases it
	// instead of the birth date.
	AgeOver *apiAgeOver `json:"ageOver,omitempty"`
}

// apiAgeOver is whether the holder is Age or older on the day of the check.
type apiAgeOver struct {
	Age  int  `json:"age"`
	Over bool `json:"over"`
}

type apiEvidenceEntry struct {
	Type         string         `json:"type"`
	DocumentType string         `json:"documentType,omitempty"`
	DocumentKind string         `json:"documentKind,omitempty"`
	IssuingState string         `json:"issuingState,omitempty"`
	ExpiryDate   string         `json:"expiryDate,omitempty"`
	PassiveAuth  asked[string]  `json:"passiveAuth,omitzero"`
	ActiveAuth   asked[string]  `json:"activeAuth,omitzero"`
	FaceMatch    asked[float64] `json:"faceMatch,omitzero"`
	Liveness     asked[string]  `json:"liveness,omitzero"`
	// DocumentCheck is the document scan's result on a photographed
	// document.
	DocumentCheck asked[apiDocumentCheck] `json:"documentCheck,omitzero"`
}

// asked is a result field its flow may ask for: absent when the flow did not
// ask for it, null when it asked and got nothing.
type asked[T any] struct {
	on    bool
	value *T
}

// askedField is value as a field the flow asked for when on.
func askedField[T any](on bool, value *T) asked[T] {
	if !on {
		return asked[T]{}
	}
	return asked[T]{on: true, value: value}
}

// askedString is askedField for a string, empty being nothing.
func askedString(on bool, value string) asked[string] {
	if value == "" {
		return askedField[string](on, nil)
	}
	return askedField(on, &value)
}

func (a asked[T]) IsZero() bool { return !a.on }

func (a asked[T]) MarshalJSON() ([]byte, error) { return json.Marshal(a.value) }

func (a *asked[T]) UnmarshalJSON(data []byte) error {
	a.on = true
	return json.Unmarshal(data, &a.value)
}

// Value is the field's value; nil when not asked or not got.
func (a asked[T]) Value() *T { return a.value }

// identityAsked is what identity's flow asked for; with its flow unknown,
// whatever the identity carries.
func identityAsked(identity proofingprovider.Identity) proofingprovider.Asked {
	if identity.Asked != nil {
		return *identity.Asked
	}
	ev := identity.Evidence
	return proofingprovider.Asked{
		Nationality: identity.Nationality != "", DutchNationality: identity.DutchNationality != nil,
		BSN:           identity.PersonalNumber != "",
		ChipChecks:    ev != nil && ev.Type == proofingprovider.EvidenceEMRTD,
		Face:          ev != nil && (ev.FaceMatch != nil || ev.Liveness != ""),
		DocumentCheck: ev != nil && ev.DocumentVerdict != "",
	}
}

// apiDocumentCheck is a document scan's result: its verdict (authentic,
// doubtful or fake) and score, and whether the fields it validated were
// valid and its zones (MRZ, printed text) read the same (valid, invalid or
// not_performed), and the fields whose zones read differently.
type apiDocumentCheck struct {
	Verdict       string   `json:"verdict"`
	Score         *float64 `json:"score,omitempty"`
	Fields        string   `json:"fields"`
	Zones         string   `json:"zones"`
	ZonesMismatch []string `json:"zonesMismatch,omitempty"`
}

func (h *Handler) apiSessionResult(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	out, err := sessionResult(r.Context(), h.service, callerFromContext(r.Context()).Org.ID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// resultReader reads an external session's result and the diplomas it holds.
type resultReader interface {
	RequestResult(ctx context.Context, orgID, id uuid.UUID) (Request, proofingprovider.Identity, error)
	RequestDiplomas(ctx context.Context, requestIDs []uuid.UUID) (map[uuid.UUID][]Diploma, error)
}

// sessionResult is the org's external session id's result as the API and the
// session.verified webhook carry it; each read is audited result_read.
func sessionResult(ctx context.Context, results resultReader, orgID, id uuid.UUID) (apiResultResponse, error) {
	req, identity, err := results.RequestResult(ctx, orgID, id)
	if err != nil {
		return apiResultResponse{}, err
	}
	out := newAPIResultResponse(req, identity, time.Now())
	diplomas, err := results.RequestDiplomas(ctx, []uuid.UUID{req.ID})
	if err != nil {
		return apiResultResponse{}, err
	}
	out.Diplomas = newDiplomaResponses(diplomas[req.ID])
	return out, nil
}

// diplomaResponses are the extracts req holds.
func (h *Handler) diplomaResponses(r *http.Request, req Request) ([]diplomaResponse, error) {
	diplomas, err := h.service.RequestDiplomas(r.Context(), []uuid.UUID{req.ID})
	if err != nil {
		return nil, mapError(err)
	}
	return newDiplomaResponses(diplomas[req.ID]), nil
}

// requestResult is an org admin's view of an external request's identity in the
// wallet, in the API's shape plus the face images; each view is
// audited.
func (h *Handler) requestResult(w http.ResponseWriter, r *http.Request) error {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	req, identity, err := h.service.AdminRequestResult(r.Context(), orgFromRequest(r).ID, id, reviewScope(r.Context()))
	if err != nil {
		return mapError(err)
	}
	out := adminResultResponse{apiResultResponse: newAPIResultResponse(req, identity, time.Now())}
	if out.Diplomas, err = h.diplomaResponses(r, req); err != nil {
		return err
	}
	// A check in review holds its identity and photos until it is decided,
	// whether or not its flow keeps the photos after (the engine drops them
	// then): what the reviewer compares. The API shows neither before an approval.
	if identity.Status == proofingprovider.StatusNeedsReview {
		out.Identity = newAPIIdentity(identity)
		out.withDocumentImages(identity)
	}
	if identity.Status == proofingprovider.StatusApproved || identity.Status == proofingprovider.StatusNeedsReview {
		out.Selfie = newAdminImage(identity.Selfie)
		out.ReferencePhoto = newAdminImage(identity.ReferencePhoto)
		out.ZoneDiffs = identity.ZoneDiffs
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// adminResultResponse is the API's result as an admin reads it in the
// wallet: for a check waiting for review also its identity, the document's
// images and the selfie, and for both the sender's reference photo and what
// the document's zones read where they differ.
type adminResultResponse struct {
	apiResultResponse
	// ReferencePhoto is the sender's own photo the selfie was matched
	// against, for a flow without the chip read.
	ReferencePhoto *adminImage `json:"referencePhoto,omitempty"`
	// ZoneDiffs is what the document scan's zones read of the fields that
	// differ: for the wallet only, never the API.
	ZoneDiffs []proofingprovider.ZoneDiff `json:"zoneDiffs,omitempty"`
}

// withDocumentImages sets the document's portrait and the photos of its
// front and back from identity.
func (out *apiResultResponse) withDocumentImages(identity proofingprovider.Identity) {
	out.Photo = newAdminImage(identity.Photo)
	out.DocumentImage = newAdminImage(identity.DocumentImage)
	out.DocumentImageBack = newAdminImage(identity.DocumentImageBack)
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

// newAPIIdentity is identity as a result shows it, the age check's answer
// included.
func newAPIIdentity(identity proofingprovider.Identity) *apiIdentity {
	want := identityAsked(identity)
	out := &apiIdentity{
		GivenName: identity.GivenName, FamilyName: identity.FamilyName, BirthDate: identity.BirthDate,
		Nationality:      askedString(want.Nationality, identity.Nationality),
		BSN:              askedString(want.BSN, identity.PersonalNumber),
		DutchNationality: askedField(want.DutchNationality, identity.DutchNationality),
	}
	if a := identity.AgeOver; a != nil {
		out.AgeOver = &apiAgeOver{Age: a.Age, Over: a.Over}
	}
	return out
}

// newAPIResultResponse is a settled request's result: the identity only for an
// approval.
func newAPIResultResponse(req Request, identity proofingprovider.Identity, now time.Time) apiResultResponse {
	out := apiResultResponse{
		ID: publicSessionID(req.ID), Status: req.EffectiveStatus(now), Method: string(identity.Method),
		AssuranceLevel: identity.AssuranceLevel, EIDASLevel: identity.EIDASLevel, ErrorCode: identity.ErrorCode,
		ReviewReason: req.ReviewReason, Steps: req.Steps(now),
		Evidence: []apiEvidenceEntry{}, Diplomas: []diplomaResponse{}, AddressReview: req.AddressReview,
		Employment: req.SubjectEmployment,
	}
	if req.SubjectAddress != nil {
		addr := req.SubjectAddress.forIntegrator()
		out.Address = &addr
	}
	if identity.Status == proofingprovider.StatusApproved {
		out.VerifiedAt = identity.CompletedAt
		out.Identity = newAPIIdentity(identity)
		out.withDocumentImages(identity)
		out.Selfie = newAdminImage(identity.Selfie)
	}
	if ev := identity.Evidence; ev != nil {
		want := identityAsked(identity)
		entry := apiEvidenceEntry{
			Type: ev.Type, DocumentType: ev.DocumentType, DocumentKind: ev.DocumentKind, IssuingState: ev.IssuingState, ExpiryDate: ev.ExpiryDate,
			FaceMatch: askedField(want.Face, ev.FaceMatch), Liveness: askedString(want.Face, ev.Liveness),
			PassiveAuth: askedField[string](want.ChipChecks, nil), ActiveAuth: askedField[string](want.ChipChecks, nil),
			DocumentCheck: askedField[apiDocumentCheck](want.DocumentCheck, nil),
		}
		// Only a chip read has chip checks; on other evidence they never apply.
		if ev.Type == proofingprovider.EvidenceEMRTD {
			entry.PassiveAuth, entry.ActiveAuth = askedString(want.ChipChecks, ev.PassiveAuth), askedString(want.ChipChecks, ev.ActiveAuth)
		}
		if ev.DocumentVerdict != "" {
			entry.DocumentCheck = askedField(want.DocumentCheck, &apiDocumentCheck{Verdict: ev.DocumentVerdict, Score: ev.DocumentScore, Fields: ev.DocumentFields, Zones: ev.DocumentZones, ZonesMismatch: ev.DocumentZonesMismatch})
		}
		out.Evidence = append(out.Evidence, entry)
	}
	return out
}

func (h *Handler) apiCancelSession(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	req, err := h.service.CancelRequest(r.Context(), caller.Org.ID, id)
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
	if err := h.service.PurgeRequest(r.Context(), caller.Org.ID, id); err != nil {
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
	req, err := h.service.ExternalRequest(r.Context(), caller.Org.ID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPISessionResponse(req, time.Now()))
	return nil
}
