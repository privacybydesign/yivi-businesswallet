package proofing

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// Handler serves identity proofing: the org routes (any member reads the flows
// made available to them and sends requests; defining flows, choosing which
// members may use, and managing the org's settings, API keys and webhook is
// admin-only), the API and the hosted page. The org is the proofing engine's
// tenant under its own id; nothing is provisioned first.
type Handler struct {
	service     *Service
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
	// apiCalls and apiSessions rate-limit the public API per org,
	// hostedCalls the hosted page per org, across all its links.
	apiCalls    *ratelimit.Limiter
	apiSessions *ratelimit.Limiter
	hostedCalls *ratelimit.Limiter
	// staleLinks holds each hosted link no longer live on its own, off its
	// org's hostedCalls.
	staleLinks *ratelimit.Limiter
	// authFailures holds unknown API keys and hosted links per source
	// (failureLimited).
	authFailures *ratelimit.Limiter
	// hostedSources holds the hosted page per source, valid links included.
	hostedSources *ratelimit.Limiter
	// webhookTests holds an admin's test events per org: each one is a
	// delivery the shared deliverer sends.
	webhookTests *ratelimit.Limiter
	// idempotency keeps API POST answers by Idempotency-Key; nil runs every
	// call.
	idempotency *IdempotencyStore
	// platformAdmins may pause any org's proofing.
	platformAdmins auth.PlatformAdmins
	// appBranding dresses the hosted page as the Idem app dresses the
	// session; nil leaves the page the org's own colour only.
	appBranding *AppBrandingResolver
}

// SetIdempotencyStore turns on Idempotency-Key handling for the API.
func (h *Handler) SetIdempotencyStore(s *IdempotencyStore) { h.idempotency = s }

// SetAppBranding gives the hosted page the branding the Idem app shows its
// session in: the proofing look, the flow's own over the org's, when it has
// one, else the org's theme.
func (h *Handler) SetAppBranding(r *AppBrandingResolver) { h.appBranding = r }

func NewHandler(service *Service, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{
		service: service, requireUser: requireUser, authorize: authorize,
		apiCalls: ratelimit.New(apiCallLimit), apiSessions: ratelimit.New(APISessionLimit),
		hostedCalls:  ratelimit.New(hostedCallLimit),
		authFailures: newAuthFailures(), hostedSources: ratelimit.New(hostedSourceLimit),
		webhookTests: ratelimit.New(webhookTestLimit), staleLinks: ratelimit.New(staleLinkLimit),
	}
}

func newAuthFailures() *ratelimit.Limiter { return ratelimit.New(authFailureLimit) }

func (h *Handler) Register(mux *http.ServeMux) {
	// A paused org's routes stay open: the service refuses only what starts
	// proofing (CreateRequest, startHosted), so an admin can still revoke a
	// key, remove a webhook or erase data, and running sessions still settle.
	member := func(next http.Handler) http.Handler { return h.requireUser(h.authorize(next)) }
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows", member(respond.HandlerFunc(h.listFlows)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows", admin(respond.HandlerFunc(h.createFlow)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.listFlowVersions)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/hosted", admin(respond.HandlerFunc(h.getFlowHosted)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/hosted", admin(respond.HandlerFunc(h.saveFlowHosted)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/branding", admin(respond.HandlerFunc(h.getFlowBranding)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/branding", admin(respond.HandlerFunc(h.saveFlowBranding)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/branding/logo", admin(respond.HandlerFunc(h.serveFlowLogo)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/diplomas", admin(respond.HandlerFunc(h.getFlowDiplomas)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/diplomas", admin(respond.HandlerFunc(h.saveFlowDiplomas)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/address", admin(respond.HandlerFunc(h.getFlowAddress)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/address", admin(respond.HandlerFunc(h.saveFlowAddress)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.editFlow)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions/{version}/activate", admin(respond.HandlerFunc(h.activateFlowVersion)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flow-selection", admin(respond.HandlerFunc(h.configureFlows)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/data-request-flows", admin(respond.HandlerFunc(h.listDataRequestFlows)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.listRequests)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.createRequest)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/stats", member(respond.HandlerFunc(h.stats)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}", member(respond.HandlerFunc(h.getRequest)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/events", member(respond.HandlerFunc(h.requestEvents)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/yivi/start", member(respond.HandlerFunc(h.startYivi)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/claim-link", member(respond.HandlerFunc(h.claimLink)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/app", member(respond.HandlerFunc(h.app)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/yivi/disclosure", member(respond.HandlerFunc(h.yiviDisclosure)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/diplomas", member(respond.HandlerFunc(h.addDiplomas)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/address", member(respond.HandlerFunc(h.addAddress)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/address/lookup", member(respond.HandlerFunc(h.lookUpAddress)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/review", admin(respond.HandlerFunc(h.decideReview)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/result", admin(respond.HandlerFunc(h.requestResult)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/data-matches", admin(respond.HandlerFunc(h.dataMatches)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/data-export", admin(respond.HandlerFunc(h.adminDataExport)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/settings", member(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PATCH /orgs/{slug}/identity-proofing/settings", admin(respond.HandlerFunc(h.updateSettings)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/settings/branding", admin(respond.HandlerFunc(h.saveBranding)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/settings/logo", member(respond.HandlerFunc(h.serveLogo)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/api-keys", admin(respond.HandlerFunc(h.listAPIKeys)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/api-keys", admin(respond.HandlerFunc(h.createAPIKey)))
	mux.Handle("DELETE /orgs/{slug}/identity-proofing/api-keys/{keyID}", admin(respond.HandlerFunc(h.revokeAPIKey)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/webhook", admin(respond.HandlerFunc(h.getWebhook)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/webhook", admin(respond.HandlerFunc(h.saveWebhook)))
	mux.Handle("DELETE /orgs/{slug}/identity-proofing/webhook", admin(respond.HandlerFunc(h.removeWebhook)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/webhook/rotate-secret", admin(respond.HandlerFunc(h.rotateWebhookSecret)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/webhook/test", admin(respond.HandlerFunc(h.testWebhook)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/webhook/deliveries", admin(respond.HandlerFunc(h.listWebhookDeliveries)))
	h.registerPublicAPI(mux)
	h.registerHosted(mux)
	h.registerPause(mux)
}

func orgFromRequest(r *http.Request) Org {
	org := organization.OrgFromContext(r.Context())
	return Org{ID: org.ID, Name: org.Name}
}

type flowResponse struct {
	proofingprovider.Flow
	// flowCompletable is false for a flow a recipient cannot finish with only the
	// vcmrtd app (see flowCompletable); it cannot be made available to members.
	Completable bool `json:"completable"`
	// Allowed is the admin having made the flow available to members; Default
	// is the org's default, the one a request form preselects, whoever may use it.
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
	// DiplomaMode is whether the flow asks for DUO diploma extracts after the
	// identity check; absent on a single version (create, edit, versions),
	// whose answer does not read the wallet's setting.
	DiplomaMode DiplomaMode `json:"diplomaMode,omitempty"`
	// AddressMode is whether the flow asks for the subject's postal address
	// after the identity check; absent on a single version, as DiplomaMode.
	AddressMode AddressMode `json:"addressMode,omitempty"`
	// flowNeedsReferencePhoto is a flow that matches the face without reading the
	// chip: only the API can send it, with the sender's own photo.
	NeedsReferencePhoto bool `json:"needsReferencePhoto"`
	// Kind is what the flow's sessions are for (an identity check, or a
	// person asking for their data or its erasure); absent on a single
	// version, as DiplomaMode.
	Kind FlowKind `json:"kind,omitempty"`
	// HostedEnabled is the flow's "Allow sending this flow as a link" switch:
	// off, the Verify a person dialog offers only Show on this screen. Absent
	// on a single version, as DiplomaMode.
	HostedEnabled *bool `json:"hostedEnabled,omitempty"`
	// RetentionDays is how many days an external person's data is kept at most
	// on this flow, as the on-screen page tells them; absent on a single version.
	RetentionDays int `json:"retentionDays,omitempty"`
}

func newFlowResponse(f OrgFlow) flowResponse {
	if f.Steps == nil {
		f.Steps = []string{}
	}
	return flowResponse{
		Flow: f.Flow, Completable: flowCompletable(f.Flow), Allowed: f.Allowed, Default: f.Default, DiplomaMode: f.Diplomas,
		AddressMode: f.Address, NeedsReferencePhoto: flowNeedsReferencePhoto(f.Flow), Kind: f.Kind, RetentionDays: f.RetentionDays,
	}
}

// newListedFlowResponse is a flow as a flow list has it, with the wallet's
// settings for it.
func newListedFlowResponse(f OrgFlow) flowResponse {
	out := newFlowResponse(f)
	out.HostedEnabled = &f.HostedEnabled
	return out
}

// listFlows shows an admin every flow of the org and a member the ones the admin
// made available to them. Neither sees the data request flows (See my data,
// Delete my data): they are the wallet's, not flows anyone edits or picks.
func (h *Handler) listFlows(w http.ResponseWriter, r *http.Request) error {
	flows, err := h.service.Flows(r.Context(), orgFromRequest(r), flowView(r))
	if err != nil {
		return mapError(err)
	}
	out := make([]flowResponse, 0, len(flows))
	for _, f := range flows {
		if f.Kind.dataRequest() {
			continue
		}
		out = append(out, newListedFlowResponse(f))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// listDataRequestFlows shows an admin the org's See my data and Delete my
// data flows, creating them the first time.
func (h *Handler) listDataRequestFlows(w http.ResponseWriter, r *http.Request) error {
	flows, err := h.service.DataRequestFlows(r.Context(), orgFromRequest(r))
	if err != nil {
		return mapError(err)
	}
	out := make([]flowResponse, 0, len(flows))
	for _, f := range flows {
		out = append(out, newListedFlowResponse(f))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type flowSelectionRequest struct {
	FlowIDs       []string `json:"flowIds"`
	DefaultFlowID string   `json:"defaultFlowId"`
}

func (h *Handler) configureFlows(w http.ResponseWriter, r *http.Request) error {
	var req flowSelectionRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	org := orgFromRequest(r)
	if err := h.service.ConfigureFlows(r.Context(), org, FlowSelection(req)); err != nil {
		return mapError(err)
	}
	return h.listFlows(w, r)
}

func (h *Handler) createFlow(w http.ResponseWriter, r *http.Request) error {
	var spec proofingprovider.FlowSpec
	if err := decode(r, &spec); err != nil {
		return err
	}
	flow, err := h.service.CreateFlow(r.Context(), orgFromRequest(r), spec)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, newFlowResponse(OrgFlow{Flow: flow}))
	return nil
}

// editFlow saves the body as the next version of the flow, active at once.
func (h *Handler) editFlow(w http.ResponseWriter, r *http.Request) error {
	var spec proofingprovider.FlowSpec
	if err := decode(r, &spec); err != nil {
		return err
	}
	flow, err := h.service.EditFlow(r.Context(), orgFromRequest(r), r.PathValue("flowID"), spec)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, newFlowResponse(OrgFlow{Flow: flow}))
	return nil
}

func (h *Handler) listFlowVersions(w http.ResponseWriter, r *http.Request) error {
	versions, err := h.service.FlowVersions(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	out := make([]flowResponse, 0, len(versions))
	for _, f := range versions {
		out = append(out, newFlowResponse(OrgFlow{Flow: f}))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) activateFlowVersion(w http.ResponseWriter, r *http.Request) error {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "version must be a positive number"}
	}
	flow, err := h.service.ActivateFlowVersion(r.Context(), orgFromRequest(r), r.PathValue("flowID"), version)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFlowResponse(OrgFlow{Flow: flow}))
	return nil
}

type requestResponse struct {
	ID              uuid.UUID  `json:"id"`
	RequestedByName string     `json:"requestedByName"`
	APIKeyName      string     `json:"apiKeyName,omitempty"`
	SubjectUserID   *uuid.UUID `json:"subjectUserId,omitempty"`
	// External is a request for an external person, not a member.
	External     bool   `json:"external"`
	SubjectName  string `json:"subjectName"`
	SubjectEmail string `json:"subjectEmail"`
	// ProofedName is the name read off an external person's approved document,
	// until its retention clears it.
	ProofedName string `json:"proofedName,omitempty"`
	FlowID      string `json:"flowId"`
	FlowName    string `json:"flowName"`
	FlowVersion int    `json:"flowVersion,omitempty"`
	// Method is the app the session was created for, then the one the engine reports
	// the subject used; absent on a request from before the choice existed.
	Method         string     `json:"method,omitempty"`
	Status         Status     `json:"status"`
	AssuranceLevel string     `json:"assuranceLevel,omitempty"`
	EIDASLevel     string     `json:"eidasLevel,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	LinkExpiresAt  time.Time  `json:"linkExpiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	// PurgeAt is when the session's personal data is due to be purged;
	// PurgedAt when it was.
	PurgeAt  *time.Time `json:"purgeAt,omitempty"`
	PurgedAt *time.Time `json:"purgedAt,omitempty"`
	// DiplomaMode is whether the request asks for DUO diploma extracts;
	// Diplomas the ones it holds, and DiplomasUntil when the subject can last
	// add one (absent until the identity is approved).
	DiplomaMode   DiplomaMode       `json:"diplomaMode"`
	Diplomas      []diplomaResponse `json:"diplomas"`
	DiplomasUntil *time.Time        `json:"diplomasUntil,omitempty"`
	// AddressMode is whether the request asks for the subject's postal
	// address; Address the one they entered, and AddressUntil when they can
	// last enter it (absent until the identity is approved, and once entered).
	AddressMode  AddressMode `json:"addressMode"`
	Address      *Address    `json:"address,omitempty"`
	AddressUntil *time.Time  `json:"addressUntil,omitempty"`
	// ExpectedSubject is a request for one known person: only subjectName,
	// born on the date it was sent with, is approved.
	ExpectedSubject bool `json:"expectedSubject"`
	// FlowKind is what the session is for; a data request goes to review once
	// the person is proven. DataExportUntil is until when an approved "see my
	// data" request's data downloads.
	FlowKind        FlowKind   `json:"flowKind"`
	DataExportUntil *time.Time `json:"dataExportUntil,omitempty"`
}

// withDiplomas adds the extracts a request holds to its response.
func (r requestResponse) withDiplomas(diplomas []Diploma) requestResponse {
	r.Diplomas = newDiplomaResponses(diplomas)
	return r
}

func newRequestResponse(req Request, now time.Time) requestResponse {
	return requestResponse{
		DiplomaMode: diplomaModeOf(req), Diplomas: []diplomaResponse{}, DiplomasUntil: diplomasUntil(req),
		AddressMode: addressModeOf(req), Address: req.SubjectAddress, AddressUntil: addressUntil(req),
		ID: req.ID, RequestedByName: req.RequestedByName, APIKeyName: req.APIKeyName, SubjectUserID: req.SubjectUserID,
		External:    req.External(),
		SubjectName: req.SubjectName, SubjectEmail: req.SubjectEmail, ProofedName: req.ProofedName,
		FlowID: req.FlowID, FlowName: req.FlowName, FlowVersion: req.FlowVersion, Method: string(req.Method), Status: req.EffectiveStatus(now),
		AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel, ErrorCode: req.ErrorCode,
		LinkExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		PurgeAt: req.PurgeAt, PurgedAt: req.PurgedAt, ExpectedSubject: req.ExpectsSubject,
		FlowKind: req.FlowKind, DataExportUntil: req.DataExportUntil,
	}
}

// listRequests shows an admin every request of the org and a member the ones they
// sent; ?subjectUserId= narrows either to the ones proofing one member,
// ?subjectKind= to those sent to members or to external people, and ?flowId= to
// those sent on one flow.
func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var filter RequestFilter
	if !organization.ActsAsAdmin(r.Context()) {
		id := auth.UserFromContext(r.Context()).ID
		filter.RequestedBy = &id
	}
	if raw := r.URL.Query().Get("subjectUserId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid subject user id"}
		}
		filter.SubjectUserID = &id
	}
	if raw := r.URL.Query().Get("subjectKind"); raw != "" {
		kind := SubjectKind(raw)
		if kind != SubjectMember && kind != SubjectExternal {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "subjectKind must be member or external"}
		}
		filter.SubjectKind = &kind
	}
	if raw := r.URL.Query().Get("flowId"); raw != "" {
		filter.FlowID = &raw
	}
	reqs, err := h.service.Requests(r.Context(), org.ID, filter)
	if err != nil {
		return mapError(err)
	}
	ids := make([]uuid.UUID, 0, len(reqs))
	for _, req := range reqs {
		ids = append(ids, req.ID)
	}
	diplomas, err := h.service.RequestDiplomas(r.Context(), ids)
	if err != nil {
		return mapError(err)
	}
	now := time.Now()
	out := make([]requestResponse, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, newRequestResponse(req, now).withDiplomas(diplomas[req.ID]))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// parseRequestID reads a request id as the dashboard has it (a UUID) or as the
// API and webhooks show it (a ps_ id), so either can be looked up.
func parseRequestID(s string) (uuid.UUID, error) {
	if id, ok := parsePublicSessionID(s); ok {
		return id, nil
	}
	return uuid.Parse(s)
}

// sentRequestTarget is the request a per-request route names, and the caller
// it is scoped to: nil for an admin, who reaches every request of the org, else
// the member, who reaches only the ones they sent.
func sentRequestTarget(r *http.Request) (uuid.UUID, *uuid.UUID, error) {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return uuid.Nil, nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	if organization.ActsAsAdmin(r.Context()) {
		return id, nil, nil
	}
	caller := auth.UserFromContext(r.Context()).ID
	return id, &caller, nil
}

// getRequest is one request, re-checked in the engine: what the on-screen page polls.
func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	req, err := h.service.Request(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	if err != nil {
		return mapError(err)
	}
	diplomas, err := h.service.RequestDiplomas(r.Context(), []uuid.UUID{req.ID})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newRequestResponse(req, time.Now()).withDiplomas(diplomas[req.ID]))
	return nil
}

// reviewDecisionRequest is an admin's decision on a request under review:
// decision "approve" or "reject", a required reason, and for a rejection an
// optional errorCode.
type reviewDecisionRequest struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	ErrorCode string `json:"errorCode"`
	// RequestIDs are the matched sessions approving a data request takes;
	// absent takes them all.
	RequestIDs []uuid.UUID `json:"requestIds"`
}

func (h *Handler) decideReview(w http.ResponseWriter, r *http.Request) error {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	var body reviewDecisionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Decision != "approve" && body.Decision != "reject" {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "decision is approve or reject"}
	}
	req, err := h.service.DecideReview(r.Context(), orgFromRequest(r).ID, id,
		string(auth.UserFromContext(r.Context()).Email),
		ReviewInput{Approve: body.Decision == "approve", ErrorCode: body.ErrorCode, Reason: body.Reason, RequestIDs: body.RequestIDs})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newRequestResponse(req, time.Now()))
	return nil
}

// claimLinkResponse is a fresh vcmrtd link for the Idem app, shown as a QR.
type claimLinkResponse struct {
	DeepLink  string    `json:"deepLink"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (h *Handler) claimLink(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	claim, err := h.service.ClaimLink(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, claimLinkResponse{DeepLink: claim.DeepLink, ExpiresAt: claim.ExpiresAt})
	return nil
}

// appResponse is where a running Idem request's phone is: waiting, connected
// or away.
type appResponse struct {
	App proofingprovider.App `json:"app"`
}

func (h *Handler) app(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	app, err := h.service.App(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, appResponse{App: app})
	return nil
}

type yiviStartResponse struct {
	// WalletLink is the openid4vp:// request the subject's Yivi app opens.
	WalletLink string    `json:"walletLink"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (h *Handler) startYivi(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	started, err := h.service.StartYivi(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, yiviStartResponse(started))
	return nil
}

type yiviDisclosureResponse struct {
	// Done is false while the subject has not finished in the Yivi app.
	Done bool `json:"done"`
	// OK is the disclosure approving the session; false rejected it, and Code
	// says why.
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
}

func (h *Handler) yiviDisclosure(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	disclosure, err := h.service.YiviDisclosure(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	return writeYiviDisclosure(w, r, disclosure, err)
}

// writeYiviDisclosure answers a Yivi disclosure poll: not done while pending.
func writeYiviDisclosure(w http.ResponseWriter, r *http.Request, disclosure proofingprovider.YiviDisclosure, err error) error {
	if errors.Is(err, ErrDisclosurePending) {
		respond.JSON(w, r, http.StatusOK, yiviDisclosureResponse{})
		return nil
	}
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, yiviDisclosureResponse{
		Done: true, OK: disclosure.OK, Code: disclosure.Code,
	})
	return nil
}

// requestEvents is one request's timeline, oldest first: every audit event
// about it (sent, session created and started, outcome, expiry). An admin sees
// any request's in full, a member only one they sent, trimmed (memberTimeline).
func (h *Handler) requestEvents(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	events, err := h.service.RequestEvents(r.Context(), orgFromRequest(r).ID, id, requestedBy)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, struct {
		Events []audit.Event `json:"events"`
	}{events})
	return nil
}

// createRequestRequest names a member (userId), or an external person by
// e-mail address and optional name (with a birth date: only that person).
// Method (idem_app, the default, or yivi_app) is the app the subject proofs
// with, Channel (email, the default, or on_screen) how the session reaches them.
type createRequestRequest struct {
	UserID  uuid.UUID               `json:"userId"`
	Email   string                  `json:"email"`
	Name    string                  `json:"name"`
	FlowID  string                  `json:"flowId"`
	Method  proofingprovider.Method `json:"method"`
	Channel Channel                 `json:"channel"`
	// FlowKind data_access or data_erasure sends a data request, on the org's
	// standard flow of that kind, instead of an identity check on FlowID.
	FlowKind FlowKind `json:"flowKind"`
	// Language is the sender's wallet language (en/nl).
	Language email.Locale `json:"language"`
	// BirthDate (YYYY-MM-DD) with Name makes an external request one for
	// that person only.
	BirthDate string `json:"birthDate"`
}

type createRequestResponse struct {
	requestResponse
	// MailSent is false when the org's mail could not be sent; the request
	// stands, but the recipient never got its link.
	MailSent bool `json:"mailSent"`
	// DeepLink is an on-screen Idem session's vcmrtd link, for the page to show
	// as the QR code; absent for a mailed request and a Yivi one.
	DeepLink          string     `json:"deepLink,omitempty"`
	DeepLinkExpiresAt *time.Time `json:"deepLinkExpiresAt,omitempty"`
	// HostedURL is a hosted request's link (channel hosted), for the member
	// to hand the external person; valid hostedLinkTTL.
	HostedURL string `json:"hostedUrl,omitempty"`
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) error {
	var body createRequestRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	sent, err := h.service.CreateRequest(r.Context(), orgFromRequest(r),
		Requester{UserID: caller.ID, Name: displayName(caller), Admin: organization.ActsAsAdmin(r.Context())}, NewRequest{
			SubjectUserID: body.UserID,
			SubjectEmail:  body.Email, SubjectName: body.Name, SubjectBirthDate: body.BirthDate, FlowID: body.FlowID,
			FlowKind: body.FlowKind, Method: body.Method, Channel: body.Channel, Language: body.Language,
		})
	if err != nil {
		return mapError(err)
	}
	out := createRequestResponse{
		requestResponse: newRequestResponse(sent.Request, time.Now()), MailSent: sent.MailSent, HostedURL: sent.HostedURL,
	}
	if body.Channel == ChannelOnScreen && sent.DeepLink != "" {
		out.DeepLink, out.DeepLinkExpiresAt = sent.DeepLink, &sent.DeepLinkExpiresAt
	}
	respond.JSON(w, r, http.StatusCreated, out)
	return nil
}

func displayName(u user.User) string {
	if u.PreferredName != nil && strings.TrimSpace(*u.PreferredName) != "" {
		return strings.TrimSpace(*u.PreferredName)
	}
	if name := strings.TrimSpace(u.GivenNames + " " + u.LastName); name != "" {
		return name
	}
	return string(u.Email)
}

type statsRowResponse struct {
	FlowID      string `json:"flowId"`
	Sessions    int    `json:"sessions"`
	Approved    int    `json:"approved"`
	Rejected    int    `json:"rejected"`
	NeedsReview int    `json:"needsReview"`
	Expired     int    `json:"expired"`
	Cancelled   int    `json:"cancelled"`
}

type statsResponse struct {
	Since time.Time          `json:"since"`
	Rows  []statsRowResponse `json:"rows"`
}

// stats counts the external requests of the last statsWindow per flow: an
// admin's over every request of the org, a member's over the ones they sent, as
// listRequests shows them.
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var requestedBy *uuid.UUID
	if !organization.ActsAsAdmin(r.Context()) {
		id := auth.UserFromContext(r.Context()).ID
		requestedBy = &id
	}
	rows, since, err := h.service.Stats(r.Context(), org.ID, requestedBy)
	if err != nil {
		return mapError(err)
	}
	out := statsResponse{Since: since, Rows: make([]statsRowResponse, 0, len(rows))}
	for _, row := range rows {
		out.Rows = append(out.Rows, statsRowResponse(row))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func decode(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid request body"}
	}
	return nil
}

func mapError(err error) error {
	var rejected *proofingprovider.RejectedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidInput):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: strings.TrimPrefix(err.Error(), ErrInvalidInput.Error()+": ")}
	case errors.Is(err, ErrNoEncryptionKey):
		return &respond.APIError{Status: http.StatusConflict, Code: "no_encryption_key", Message: "this deployment has no identity proofing encryption key configured; set IDENTITY_PROOFING_ENCRYPTION_KEY on the server"}
	case errors.Is(err, ErrFlowNotFound):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_found", Message: "the selected flow does not exist"}
	case errors.Is(err, ErrFlowNotCompletable):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_completable", Message: "this flow cannot be finished from the wallet: its face step needs the NFC chip read to compare against and must run in the app"}
	case errors.Is(err, ErrMemberNotFound):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "member_not_found", Message: "that person is not a member of this organization"}
	case errors.Is(err, ErrAPIKeyNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "api_key_not_found", Message: "this API key does not exist"}
	case errors.Is(err, ErrRequestNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "this session does not exist"}
	case errors.Is(err, ErrWebhookNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "webhook_not_found", Message: "this organization has no webhook endpoint"}
	case errors.Is(err, ErrNoDefaultFlow):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "no_default_flow", Message: "name a flow: the organization has no default flow"}
	case errors.Is(err, ErrNoLogo):
		return &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "no logo set"}
	case errors.Is(err, ErrFlowNotAllowed):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_allowed", Message: "your organization's admin has not made this flow available"}
	case errors.Is(err, ErrWrongMethod):
		return &respond.APIError{Status: http.StatusConflict, Code: "wrong_method", Message: "this session does not run in that app"}
	case errors.Is(err, ErrNotHosted):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_hosted", Message: "create the session with hosted: true to pick its app from your own UI"}
	case errors.Is(err, ErrResultNotReady):
		return &respond.APIError{Status: http.StatusConflict, Code: "result_not_ready", Message: "this session has no outcome yet"}
	case errors.Is(err, ErrDeviceActive):
		return &respond.APIError{Status: http.StatusConflict, Code: "device_active", Message: "the Idem app still has this session open; carry on there"}
	case errors.Is(err, ErrSessionOver):
		return &respond.APIError{Status: http.StatusConflict, Code: "session_over", Message: "this session has ended"}
	case errors.Is(err, ErrNotUnderReview):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_under_review", Message: "this request is not under review"}
	case errors.Is(err, ErrLinkStarted):
		return &respond.APIError{Status: http.StatusConflict, Code: "link_started", Message: "this link was started already"}
	case errors.Is(err, ErrDiplomasNotAsked):
		return &respond.APIError{Status: http.StatusConflict, Code: "diplomas_not_asked", Message: "this session asks for no diplomas"}
	case errors.Is(err, ErrDiplomasClosed):
		return &respond.APIError{Status: http.StatusConflict, Code: "diplomas_closed", Message: "diplomas can be added for a while after the identity is approved: " + DiplomaUploadWindow.String()}
	case errors.Is(err, ErrAddressNotAsked):
		return &respond.APIError{Status: http.StatusConflict, Code: "address_not_asked", Message: "this session asks for no address"}
	case errors.Is(err, ErrAddressClosed):
		return &respond.APIError{Status: http.StatusConflict, Code: "address_closed", Message: "the address can be entered once, for a while after the identity is approved: " + AddressWindow.String()}
	case errors.Is(err, ErrAddressNotFound):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "address_not_found", Message: "this address is not in the Dutch address register: check the postal code, house number and street"}
	case errors.Is(err, ErrAddressLookupUnavailable):
		return &respond.APIError{Status: http.StatusServiceUnavailable, Code: "address_lookup_unavailable", Message: "the Dutch address register cannot be reached; enter the address in full"}
	case errors.Is(err, ErrReferencePhotoRequired):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "reference_photo_required", Message: "this flow matches the face against your own photo of the person: send it as referencePhoto through the API"}
	case errors.Is(err, ErrDiplomasNeedPage):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "diplomas_need_page", Message: "this flow asks for diplomas, which are uploaded on a page: show the session on screen or send a hosted link"}
	case errors.Is(err, ErrAddressNeedsPage):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "address_needs_page", Message: "this flow asks for the address, which is entered on a page: show the session on screen or send a hosted link"}
	case errors.Is(err, ErrHostedDisabled):
		return &respond.APIError{Status: http.StatusConflict, Code: "hosted_disabled", Message: "this flow's hosted page is switched off"}
	case errors.Is(err, ErrProofingPaused):
		return &respond.APIError{Status: http.StatusForbidden, Code: "proofing_paused", Message: "identity proofing is paused for this organisation"}
	case errors.Is(err, ErrOrgNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "organization_not_found", Message: "organisation not found"}
	case errors.Is(err, ErrFlowNoIdentity):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_no_identity", Message: "the flow must read the name and date of birth: a data request finds a person's sessions by them, and a diploma's holder is matched against them"}
	case errors.Is(err, ErrDataRequestFlow):
		return &respond.APIError{Status: http.StatusConflict, Code: "data_request_flow", Message: "See my data and Delete my data are the wallet's own flows: they cannot be changed"}
	case errors.Is(err, ErrDataFlowForMember):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "data_flow_for_member", Message: "a data request flow is for external people, not for members"}
	case errors.Is(err, ErrLegalRetention):
		return &respond.APIError{Status: http.StatusConflict, Code: "legal_retention", Message: "a session within the organisation's legal retention cannot be erased yet"}
	case errors.Is(err, ErrExportUnavailable):
		return &respond.APIError{Status: http.StatusGone, Code: "export_unavailable", Message: "no data is available to download for this session"}
	case errors.Is(err, ErrRedirectNotAllowed):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "redirect_not_allowed", Message: "redirectUrl must be on one of the organization's allowed redirect origins"}
	case errors.As(err, &rejected):
		// The engine's own validation message (e.g. which check a step requires) is what
		// the admin needs to fix the flow.
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "rejected_by_provider", Message: rejected.Message}
	default:
		return fmt.Errorf("identity proofing: %w", err)
	}
}

// flowView is the flows a caller sees: every one for an admin, the ones
// members may send on for a member.
func flowView(r *http.Request) FlowView {
	if organization.ActsAsAdmin(r.Context()) {
		return FlowsAll
	}
	return FlowsAllowed
}
