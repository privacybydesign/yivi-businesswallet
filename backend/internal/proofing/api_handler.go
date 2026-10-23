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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/clientip"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
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
}

func newAPIKeyResponse(k APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, CreatedByName: k.CreatedByName,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
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

const bearerPrefix = "Bearer "

// requireAPIKey authenticates the key in the Authorization header.
func (h *Handler) requireAPIKey(scope string, next respond.HandlerFunc) http.Handler {
	return respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get(headerAuthorization), bearerPrefix)
		if !ok || raw == "" {
			w.Header().Set(headerWWWAuthenticate, `Bearer realm="proofing"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "send an API key as a Bearer token"}
		}

		caller, err := h.service.AuthenticateAPIKey(r.Context(), strings.TrimSpace(raw))
		if errors.Is(err, ErrAPIKeyInvalid) {
			if err := h.failureLimited(w, r); err != nil {
				return err
			}
			w.Header().Set(headerWWWAuthenticate, `Bearer realm="proofing", error="invalid_token"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: "this API key is unknown or revoked"}
		}

		if err != nil {
			return err
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

		ctx := audit.ContextWithActor(context.WithValue(r.Context(), apiCallerKey{}, caller),
			audit.Actor{Label: APIKeyActorPrefix + caller.KeyPrefix})
		return next(w, r.WithContext(ctx))
	})
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
	// PurgedAt is when the session's data was erased (DELETE).
	PurgedAt *time.Time `json:"purgedAt,omitempty"`
	// ExpectedSubject is a session for one known person, created with a
	// birthDate: only that name and birth date are approved (IDENTITY_MISMATCH
	// otherwise).
	ExpectedSubject bool `json:"expectedSubject"`
	// FlowKind is what the session is for: identity, or data_access /
	// data_erasure, which goes to the org's review once the person is proven.
	// DataExportUntil is until when an approved data_access session's data
	// downloads (GET .../data-export).
	FlowKind        FlowKind   `json:"flowKind"`
	DataExportUntil *time.Time `json:"dataExportUntil,omitempty"`
}

func newAPISessionResponse(req Request, now time.Time) apiSessionResponse {
	return apiSessionResponse{
		ID: publicSessionID(req.ID), Status: req.EffectiveStatus(now), FlowID: req.FlowID, FlowName: req.FlowName,
		FlowVersion: req.FlowVersion, Method: string(req.Method), SubjectEmail: req.SubjectEmail, SubjectName: req.SubjectName,
		ProofedName: req.ProofedName, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel,
		ErrorCode: req.ErrorCode, ExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		CancelledAt: req.CancelledAt, PurgedAt: req.PurgedAt,
		ExpectedSubject: req.ExpectsSubject, FlowKind: req.FlowKind, DataExportUntil: req.DataExportUntil,
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

func (h *Handler) apiStartMethod(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	method := proofingprovider.Method(r.PathValue("method"))
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
	req, err := h.service.StoredExternalRequest(r.Context(), caller.Org.ID, id)
	if err != nil {
		return mapError(err)
	}
	if string(req.Method) != r.PathValue("method") {
		return mapError(ErrWrongMethod)
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
// evidence. identity is set only for an approved session; never the document
// number or an image.
type apiResultResponse struct {
	ID             string `json:"id"`
	Status         Status `json:"status"`
	Method         string `json:"method,omitempty"`
	AssuranceLevel string `json:"assuranceLevel,omitempty"`
	EIDASLevel     string `json:"eidasLevel,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	// ReviewReason is why the session went to review after its identity was
	// approved (no_current_employment, current_benefit); absent otherwise.
	ReviewReason string             `json:"reviewReason,omitempty"`
	VerifiedAt   *time.Time         `json:"verifiedAt,omitempty"`
	Identity     *apiIdentity       `json:"identity,omitempty"`
	Evidence     []apiEvidenceEntry `json:"evidence"`
	// Diplomas are the DUO diploma extracts the subject added, each checked
	// against DUO's signature and the proofed identity.
	Diplomas []diplomaResponse `json:"diplomas"`
	// Address is the postal address the subject entered after their identity
	// was approved; as they declared it, not read off a document.
	Address *Address `json:"address,omitempty"`
	// Employment is what was kept of the subject's UWV Verzekeringsbericht
	// after their identity was approved, checked against UWV's seal and the
	// proofed identity.
	Employment *Employment `json:"employment,omitempty"`
}

type apiIdentity struct {
	GivenName   string `json:"givenName,omitempty"`
	FamilyName  string `json:"familyName,omitempty"`
	BirthDate   string `json:"birthDate,omitempty"`
	Nationality string `json:"nationality,omitempty"`
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
	Type         string   `json:"type"`
	DocumentType string   `json:"documentType,omitempty"`
	DocumentKind string   `json:"documentKind,omitempty"`
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
	req, identity, err := h.service.RequestResult(r.Context(), caller.Org.ID, id)
	if err != nil {
		return mapError(err)
	}
	out := newAPIResultResponse(req, identity, time.Now())
	if out.Diplomas, err = h.diplomaResponses(r, req); err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
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
	}
	if identity.Status == proofingprovider.StatusApproved || identity.Status == proofingprovider.StatusNeedsReview {
		out.Photo, out.Selfie = newAdminImage(identity.Photo), newAdminImage(identity.Selfie)
		out.ReferencePhoto = newAdminImage(identity.ReferencePhoto)
		out.DocumentImage = newAdminImage(identity.DocumentImage)
		out.DocumentImageBack = newAdminImage(identity.DocumentImageBack)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// adminResultResponse is the API's result as an admin reads it in the
// wallet: for an approval, and for a check waiting for review, also the
// document's portrait, the live selfie matched against it and the photos of
// the document's front and back, and for the check in review its identity.
// The API never carries an image.
type adminResultResponse struct {
	apiResultResponse
	Photo  *adminImage `json:"photo,omitempty"`
	Selfie *adminImage `json:"selfie,omitempty"`
	// ReferencePhoto is the sender's own photo the selfie was matched
	// against, for a flow without the chip read.
	ReferencePhoto    *adminImage `json:"referencePhoto,omitempty"`
	DocumentImage     *adminImage `json:"documentImage,omitempty"`
	DocumentImageBack *adminImage `json:"documentImageBack,omitempty"`
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
	out := &apiIdentity{
		GivenName: identity.GivenName, FamilyName: identity.FamilyName,
		BirthDate: identity.BirthDate, Nationality: identity.Nationality,
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
		ReviewReason: req.ReviewReason,
		Evidence:     []apiEvidenceEntry{}, Diplomas: []diplomaResponse{}, Address: req.SubjectAddress,
		Employment: req.SubjectEmployment,
	}
	if identity.Status == proofingprovider.StatusApproved {
		out.VerifiedAt = identity.CompletedAt
		out.Identity = newAPIIdentity(identity)
	}
	if ev := identity.Evidence; ev != nil {
		out.Evidence = append(out.Evidence, apiEvidenceEntry{
			Type: ev.Type, DocumentType: ev.DocumentType, DocumentKind: ev.DocumentKind, IssuingState: ev.IssuingState, ExpiryDate: ev.ExpiryDate,
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
