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
// made available to them and the org's customers, and sends requests; defining
// flows, choosing which members may use, and managing customers and the flows
// assigned to them is admin-only), the customer API, the hosted page and the
// wallet's default webhook endpoint. The org is the proofing engine's tenant
// under its own id; nothing is provisioned first.
type Handler struct {
	service     *Service
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
	// apiCalls and apiSessions rate-limit the public API per customer,
	// hostedCalls the hosted page per customer, across all its links.
	apiCalls    *ratelimit.Limiter
	apiSessions *ratelimit.Limiter
	hostedCalls *ratelimit.Limiter
	// authFailures holds unknown API keys and hosted links per source
	// (failureLimited).
	authFailures *ratelimit.Limiter
	// memberFaceFrames holds the on-screen Yivi face check's frames per org:
	// each one is a Regula call.
	memberFaceFrames *ratelimit.Limiter
	// idempotency keeps customer-API POST answers by Idempotency-Key; nil
	// runs every call.
	idempotency *IdempotencyStore
	// platformAdmins may pause any org's proofing.
	platformAdmins auth.PlatformAdmins
}

// SetIdempotencyStore turns on Idempotency-Key handling for the customer API.
func (h *Handler) SetIdempotencyStore(s *IdempotencyStore) { h.idempotency = s }

func NewHandler(service *Service, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{
		service: service, requireUser: requireUser, authorize: authorize,
		apiCalls: ratelimit.New(apiCallLimit), apiSessions: ratelimit.New(APISessionLimit),
		hostedCalls: ratelimit.New(hostedCallLimit), memberFaceFrames: ratelimit.New(memberFaceFrameLimit),
		authFailures: newAuthFailures(),
	}
}

func newAuthFailures() *ratelimit.Limiter { return ratelimit.New(authFailureLimit) }

func (h *Handler) Register(mux *http.ServeMux) {
	// Every org route refuses while the org's proofing is paused; the pause
	// routes themselves are registerPause's.
	member := func(next http.Handler) http.Handler { return h.requireUser(h.authorize(h.active(next))) }
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(h.active(next))))
	}
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows", member(respond.HandlerFunc(h.listFlows)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows", admin(respond.HandlerFunc(h.createFlow)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.listFlowVersions)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/hosted", admin(respond.HandlerFunc(h.getFlowHosted)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/hosted", admin(respond.HandlerFunc(h.saveFlowHosted)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/diplomas", admin(respond.HandlerFunc(h.getFlowDiplomas)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/diplomas", admin(respond.HandlerFunc(h.saveFlowDiplomas)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/kind", admin(respond.HandlerFunc(h.getFlowKind)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flows/{flowID}/kind", admin(respond.HandlerFunc(h.saveFlowKind)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.editFlow)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions/{version}/activate", admin(respond.HandlerFunc(h.activateFlowVersion)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flow-selection", admin(respond.HandlerFunc(h.configureFlows)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.listRequests)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.createRequest)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/stats", member(respond.HandlerFunc(h.stats)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}", member(respond.HandlerFunc(h.getRequest)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/events", member(respond.HandlerFunc(h.requestEvents)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/yivi/start", member(respond.HandlerFunc(h.startYivi)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/claim-link", member(respond.HandlerFunc(h.claimLink)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/app", member(respond.HandlerFunc(h.app)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/yivi/disclosure", member(respond.HandlerFunc(h.yiviDisclosure)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/yivi/face", member(respond.HandlerFunc(h.limitMemberFace(h.faceFrame))))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/diplomas", member(respond.HandlerFunc(h.addDiplomas)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests/{requestID}/review", admin(respond.HandlerFunc(h.decideReview)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/result", admin(respond.HandlerFunc(h.requestResult)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/data-matches", admin(respond.HandlerFunc(h.dataMatches)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests/{requestID}/data-export", admin(respond.HandlerFunc(h.adminDataExport)))
	mux.Handle("GET /orgs/{slug}/customers", member(respond.HandlerFunc(h.listCustomers)))
	mux.Handle("POST /orgs/{slug}/customers", admin(respond.HandlerFunc(h.createCustomer)))
	mux.Handle("GET /orgs/{slug}/customers/{customerID}", member(respond.HandlerFunc(h.getCustomer)))
	mux.Handle("PATCH /orgs/{slug}/customers/{customerID}", admin(respond.HandlerFunc(h.updateCustomer)))
	mux.Handle("DELETE /orgs/{slug}/customers/{customerID}", admin(respond.HandlerFunc(h.removeCustomer)))
	mux.Handle("GET /orgs/{slug}/customers/{customerID}/api-keys", admin(respond.HandlerFunc(h.listAPIKeys)))
	mux.Handle("POST /orgs/{slug}/customers/{customerID}/api-keys", admin(respond.HandlerFunc(h.createAPIKey)))
	mux.Handle("DELETE /orgs/{slug}/customers/{customerID}/api-keys/{keyID}", admin(respond.HandlerFunc(h.revokeAPIKey)))
	mux.Handle("PUT /orgs/{slug}/customers/{customerID}/branding", admin(respond.HandlerFunc(h.saveBranding)))
	mux.Handle("GET /orgs/{slug}/customers/{customerID}/logo", member(respond.HandlerFunc(h.serveCustomerLogo)))
	mux.Handle("GET /orgs/{slug}/customers/{customerID}/webhook", admin(respond.HandlerFunc(h.getWebhook)))
	mux.Handle("PUT /orgs/{slug}/customers/{customerID}/webhook", admin(respond.HandlerFunc(h.saveWebhook)))
	mux.Handle("DELETE /orgs/{slug}/customers/{customerID}/webhook", admin(respond.HandlerFunc(h.removeWebhook)))
	mux.Handle("POST /orgs/{slug}/customers/{customerID}/webhook/rotate-secret", admin(respond.HandlerFunc(h.rotateWebhookSecret)))
	mux.Handle("POST /orgs/{slug}/customers/{customerID}/webhook/test", admin(respond.HandlerFunc(h.testWebhook)))
	mux.Handle("GET /orgs/{slug}/customers/{customerID}/webhook/deliveries", admin(respond.HandlerFunc(h.listWebhookDeliveries)))
	h.registerPublicAPI(mux)
	h.registerHosted(mux)
	h.registerPause(mux)
	mux.Handle("GET /orgs/{slug}/customers/{customerID}/flows", member(respond.HandlerFunc(h.listCustomerFlows)))
	mux.Handle("PUT /orgs/{slug}/customers/{customerID}/flow-selection", admin(respond.HandlerFunc(h.assignCustomerFlows)))
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
	// is the one the request form preselects.
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
	// DiplomaMode is whether the flow asks for DUO diploma extracts after the
	// identity check; absent on a single version (create, edit, versions),
	// whose answer does not read the wallet's setting.
	DiplomaMode DiplomaMode `json:"diplomaMode,omitempty"`
	// flowNeedsReferencePhoto is a flow that matches the face without reading the
	// chip: only a customer's API can send it, with its own photo.
	NeedsReferencePhoto bool `json:"needsReferencePhoto"`
	// Kind is what the flow's sessions are for (an identity check, or a
	// person asking for their data or its erasure); absent on a single
	// version, as DiplomaMode.
	Kind FlowKind `json:"kind,omitempty"`
}

func newFlowResponse(f OrgFlow) flowResponse {
	if f.Steps == nil {
		f.Steps = []string{}
	}
	return flowResponse{
		Flow: f.Flow, Completable: flowCompletable(f.Flow), Allowed: f.Allowed, Default: f.Default, DiplomaMode: f.Diplomas,
		NeedsReferencePhoto: flowNeedsReferencePhoto(f.Flow), Kind: f.Kind,
	}
}

// listFlows shows an admin every flow of the org and a member the ones the admin
// made available to them.
func (h *Handler) listFlows(w http.ResponseWriter, r *http.Request) error {
	flows, err := h.service.Flows(r.Context(), orgFromRequest(r), flowView(r))
	if err != nil {
		return mapError(err)
	}
	out := make([]flowResponse, 0, len(flows))
	for _, f := range flows {
		out = append(out, newFlowResponse(f))
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
	CustomerID      *uuid.UUID `json:"customerId,omitempty"`
	CustomerName    string     `json:"customerName,omitempty"`
	SubjectName     string     `json:"subjectName"`
	SubjectEmail    string     `json:"subjectEmail"`
	// ProofedName is the name read off a customer's subject's approved document,
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
		ID: req.ID, RequestedByName: req.RequestedByName, APIKeyName: req.APIKeyName, SubjectUserID: req.SubjectUserID,
		CustomerID: req.CustomerID, CustomerName: req.CustomerName,
		SubjectName: req.SubjectName, SubjectEmail: req.SubjectEmail, ProofedName: req.ProofedName,
		FlowID: req.FlowID, FlowName: req.FlowName, FlowVersion: req.FlowVersion, Method: string(req.Method), Status: req.EffectiveStatus(now),
		AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel, ErrorCode: req.ErrorCode,
		LinkExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
		PurgeAt: req.PurgeAt, PurgedAt: req.PurgedAt, ExpectedSubject: req.ExpectsSubject,
		FlowKind: req.FlowKind, DataExportUntil: req.DataExportUntil,
	}
}

// listRequests shows an admin every request of the org and a member the ones they
// sent; ?customerId= narrows either to one customer's, ?subjectUserId= to the
// ones proofing one member.
func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var filter RequestFilter
	if !organization.ActsAsAdmin(r.Context()) {
		id := auth.UserFromContext(r.Context()).ID
		filter.RequestedBy = &id
	}
	if raw := r.URL.Query().Get("customerId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid customer id"}
		}
		filter.CustomerID = &id
	}
	if raw := r.URL.Query().Get("subjectUserId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid subject user id"}
		}
		filter.SubjectUserID = &id
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
// customer API and webhooks show it (a ps_ id), so either can be looked up.
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
	// OK is the disclosure giving a photo to check the face against; false
	// ended the session, and Code says why.
	OK           bool   `json:"ok"`
	Code         string `json:"code,omitempty"`
	StableFrames int    `json:"stableFrames,omitempty"`
	MaxAttempts  int    `json:"maxAttempts,omitempty"`
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
		StableFrames: disclosure.StableFrames, MaxAttempts: disclosure.MaxAttempts,
	})
	return nil
}

// memberFaceFrameLimit holds the on-screen Yivi face check's frames per org,
// as hostedCallLimit holds a customer's hosted links, and per API replica. It
// fits some ten members at a face check at once, a frame every 400 ms each.
var memberFaceFrameLimit = ratelimit.Limit{Burst: 1500, Per: time.Minute}

// limitMemberFace counts a face frame against its org's memberFaceFrameLimit.
func (h *Handler) limitMemberFace(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := rateLimited(w, h.memberFaceFrames, orgFromRequest(r).ID.String()); err != nil {
			return err
		}
		return next(w, r)
	}
}

// maxFaceFrameBytes caps one camera frame as the page sends it: a JPEG data
// URL of a few hundred kilobytes at most.
const maxFaceFrameBytes = 2 << 20

type faceFrameRequest struct {
	Image string `json:"image"`
}

type faceFrameResponse struct {
	FaceDetected bool   `json:"faceDetected"`
	Matched      bool   `json:"matched"`
	Consecutive  int    `json:"consecutive"`
	StableFrames int    `json:"stableFrames"`
	Attempts     int    `json:"attempts"`
	MaxAttempts  int    `json:"maxAttempts"`
	Decision     string `json:"decision"`
}

func (h *Handler) faceFrame(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	image, err := decodeFaceFrame(w, r)
	if err != nil {
		return err
	}
	verdict, err := h.service.FaceFrame(r.Context(), orgFromRequest(r).ID, id, requestedBy, image)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFaceVerdictResponse(verdict))
	return nil
}

// decodeFaceFrame reads one camera frame, capped at maxFaceFrameBytes.
func decodeFaceFrame(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFaceFrameBytes)
	var body faceFrameRequest
	if err := decode(r, &body); err != nil {
		return "", err
	}
	if body.Image == "" {
		return "", &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "a frame needs an image"}
	}
	return body.Image, nil
}

func newFaceVerdictResponse(verdict proofingprovider.FaceVerdict) faceFrameResponse {
	return faceFrameResponse{
		FaceDetected: verdict.FaceDetected, Matched: verdict.Matched, Consecutive: verdict.Consecutive,
		StableFrames: verdict.StableFrames, Attempts: verdict.Attempts, MaxAttempts: verdict.MaxAttempts,
		Decision: string(verdict.Decision),
	}
}

// requestEvents is one request's timeline, oldest first: every audit event
// about it (sent, session created and started, outcome, expiry). An admin sees
// any request's, a member only one they sent.
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

// createRequestRequest names a member (userId), or a customer and its subject's
// e-mail address and optional name (with a birth date: only that person).
// Method (idem_app, the default, or yivi_app) is the app the subject proofs
// with, Channel (email, the default, or on_screen) how the session reaches them.
type createRequestRequest struct {
	UserID     uuid.UUID               `json:"userId"`
	CustomerID *uuid.UUID              `json:"customerId"`
	Email      string                  `json:"email"`
	Name       string                  `json:"name"`
	FlowID     string                  `json:"flowId"`
	Method     proofingprovider.Method `json:"method"`
	Channel    Channel                 `json:"channel"`
	// Language is the sender's wallet language (en/nl).
	Language email.Locale `json:"language"`
	// BirthDate (YYYY-MM-DD) with Name makes a customer's request one for
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
	// to hand the customer's subject; valid hostedLinkTTL.
	HostedURL string `json:"hostedUrl,omitempty"`
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) error {
	var body createRequestRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	sent, err := h.service.CreateRequest(r.Context(), orgFromRequest(r),
		Requester{UserID: caller.ID, Name: displayName(caller)}, NewRequest{
			SubjectUserID: body.UserID, CustomerID: body.CustomerID,
			SubjectEmail: body.Email, SubjectName: body.Name, SubjectBirthDate: body.BirthDate, FlowID: body.FlowID,
			Method: body.Method, Channel: body.Channel, Language: body.Language,
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

type customerResponse struct {
	ID            uuid.UUID      `json:"id"`
	Name          string         `json:"name"`
	FlowIDs       []string       `json:"flowIds"`
	DefaultFlowID string         `json:"defaultFlowId,omitempty"`
	Status        CustomerStatus `json:"status"`
	PausedAt      *time.Time     `json:"pausedAt,omitempty"`
	// SessionTTLSeconds is how long a mailed session runs; DataRetentionDays how
	// long an approved subject's proofed name is kept.
	SessionTTLSeconds int                   `json:"sessionTtlSeconds"`
	DataRetentionDays int                   `json:"dataRetentionDays"`
	Webhook           webhookHealthResponse `json:"webhook"`
	Branding          brandingResponse      `json:"branding"`
	// AllowedRedirectOrigins are where a hosted page may send its subject back
	// to and be embedded on.
	AllowedRedirectOrigins []string  `json:"allowedRedirectOrigins"`
	HasAPIKey              bool      `json:"hasApiKey"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

// newCustomerResponse shows a customer with its endpoint's health; a customer
// absent from health has no endpoint.
func newCustomerResponse(slug string, c Customer, health map[uuid.UUID]WebhookHealth) customerResponse {
	return customerResponse{
		ID:                c.ID,
		Name:              c.Name,
		FlowIDs:           c.Flows.FlowIDs,
		DefaultFlowID:     c.Flows.DefaultFlowID,
		Status:            c.Status(),
		PausedAt:          c.PausedAt,
		SessionTTLSeconds: int(c.Settings.SessionTTL.Seconds()),
		DataRetentionDays: c.Settings.DataRetentionDays,
		Webhook:           newWebhookHealthResponse(health[c.ID]),
		Branding:          newBrandingResponse(slug, c),
		HasAPIKey:         c.HasAPIKey,
		// Never null: the admin UI edits it as a list.
		AllowedRedirectOrigins: append([]string{}, c.RedirectOrigins...),
		CreatedAt:              c.CreatedAt,
		UpdatedAt:              c.UpdatedAt,
	}
}

func customerIDFromPath(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("customerID"))
	if err != nil {
		return uuid.Nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid customer id"}
	}
	return id, nil
}

func (h *Handler) listCustomers(w http.ResponseWriter, r *http.Request) error {
	orgID := orgFromRequest(r).ID
	customers, err := h.service.Customers(r.Context(), orgID)
	if err != nil {
		return mapError(err)
	}
	health, err := h.service.WebhookHealth(r.Context(), orgID)
	if err != nil {
		return mapError(err)
	}
	out := make([]customerResponse, 0, len(customers))
	for _, c := range customers {
		out = append(out, newCustomerResponse(r.PathValue("slug"), c, health))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type customerRequest struct {
	Name string `json:"name"`
}

func (h *Handler) createCustomer(w http.ResponseWriter, r *http.Request) error {
	var body customerRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	c, err := h.service.CreateCustomer(r.Context(), orgFromRequest(r).ID, caller.ID, body.Name)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, newCustomerResponse(r.PathValue("slug"), c, nil))
	return nil
}

func (h *Handler) getCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	orgID := orgFromRequest(r).ID
	c, err := h.service.Customer(r.Context(), orgID, id)
	if err != nil {
		return mapError(err)
	}
	return h.respondCustomer(w, r, c)
}

// respondCustomer answers with one customer and its endpoint's health.
func (h *Handler) respondCustomer(w http.ResponseWriter, r *http.Request, c Customer) error {
	health, err := h.service.WebhookHealth(r.Context(), c.OrganizationID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newCustomerResponse(r.PathValue("slug"), c, health))
	return nil
}

// updateCustomerRequest renames a customer, pauses or resumes proofing for it,
// and/or changes its session settings; an absent field is left as it is.
type updateCustomerRequest struct {
	Name              *string `json:"name"`
	Paused            *bool   `json:"paused"`
	SessionTTLSeconds *int    `json:"sessionTtlSeconds"`
	DataRetentionDays *int    `json:"dataRetentionDays"`
	// AllowedRedirectOrigins replaces the list; absent leaves it.
	AllowedRedirectOrigins *[]string `json:"allowedRedirectOrigins"`
}

func (h *Handler) updateCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}

	var body updateCustomerRequest
	if err := decode(r, &body); err != nil {
		return err
	}

	if body.Name == nil && body.Paused == nil && body.SessionTTLSeconds == nil && body.DataRetentionDays == nil &&
		body.AllowedRedirectOrigins == nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "nothing to update"}
	}

	orgID := orgFromRequest(r).ID
	var c Customer
	if body.Name != nil {
		if c, err = h.service.RenameCustomer(r.Context(), orgID, id, *body.Name); err != nil {
			return mapError(err)
		}
	}

	if body.Paused != nil {
		status := CustomerActive
		if *body.Paused {
			status = CustomerPaused
		}
		if c, err = h.service.SetCustomerStatus(r.Context(), orgID, id, status); err != nil {
			return mapError(err)
		}
	}

	if body.SessionTTLSeconds != nil || body.DataRetentionDays != nil {
		current, err := h.service.Customer(r.Context(), orgID, id)
		if err != nil {
			return mapError(err)
		}
		settings := current.Settings
		if body.SessionTTLSeconds != nil {
			settings.SessionTTL = time.Duration(*body.SessionTTLSeconds) * time.Second
		}
		if body.DataRetentionDays != nil {
			settings.DataRetentionDays = *body.DataRetentionDays
		}
		if c, err = h.service.SaveCustomerSettings(r.Context(), orgID, id, settings); err != nil {
			return mapError(err)
		}
	}

	if body.AllowedRedirectOrigins != nil {
		if c, err = h.service.SaveCustomerRedirectOrigins(r.Context(), orgID, id, *body.AllowedRedirectOrigins); err != nil {
			return mapError(err)
		}
	}

	return h.respondCustomer(w, r, c)
}

func (h *Handler) removeCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	if err := h.service.RemoveCustomer(r.Context(), orgFromRequest(r).ID, id); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type statsRowResponse struct {
	CustomerID  uuid.UUID `json:"customerId"`
	FlowID      string    `json:"flowId"`
	Sessions    int       `json:"sessions"`
	Approved    int       `json:"approved"`
	Rejected    int       `json:"rejected"`
	NeedsReview int       `json:"needsReview"`
	Expired     int       `json:"expired"`
	Cancelled   int       `json:"cancelled"`
}

type statsResponse struct {
	Since time.Time          `json:"since"`
	Rows  []statsRowResponse `json:"rows"`
}

// stats counts the customer requests of the last statsWindow per customer and
// flow: an admin's over every request of the org, a member's over the ones they
// sent, as listRequests shows them.
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

type customerFlowResponse struct {
	proofingprovider.Flow
	Completable bool `json:"completable"`
	// Assigned is the admin having assigned the flow to the customer; Default is
	// the one the request form preselects for it.
	Assigned    bool        `json:"assigned"`
	Default     bool        `json:"default"`
	DiplomaMode DiplomaMode `json:"diplomaMode"`
	// NeedsReferencePhoto: see flowResponse. flowCompletable counts it as
	// finishable (customerCompletable), sent through the customer's API.
	NeedsReferencePhoto bool `json:"needsReferencePhoto"`
	// Kind: see flowResponse.
	Kind FlowKind `json:"kind"`
	// RetentionDays is how many days a subject's data is kept at most on this
	// flow for the customer, as the on-screen page tells them.
	RetentionDays int `json:"retentionDays"`
}

// listCustomerFlows shows an admin every flow of the org with the customer's
// assignment, and a member only the flows assigned to the customer.
func (h *Handler) listCustomerFlows(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	flows, err := h.service.CustomerFlows(r.Context(), orgFromRequest(r), id, flowView(r))
	if err != nil {
		return mapError(err)
	}
	out := make([]customerFlowResponse, 0, len(flows))
	for _, f := range flows {
		if f.Steps == nil {
			f.Steps = []string{}
		}
		out = append(out, customerFlowResponse{
			Flow: f.Flow, Completable: customerCompletable(f.Flow), Assigned: f.Assigned, Default: f.Default, DiplomaMode: f.Diplomas,
			NeedsReferencePhoto: flowNeedsReferencePhoto(f.Flow), Kind: f.Kind, RetentionDays: f.RetentionDays,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) assignCustomerFlows(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	var body flowSelectionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if _, err := h.service.AssignCustomerFlows(r.Context(), orgFromRequest(r), id, FlowSelection(body)); err != nil {
		return mapError(err)
	}
	return h.listCustomerFlows(w, r)
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
	case errors.Is(err, ErrCustomerNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "customer_not_found", Message: "this customer does not exist"}
	case errors.Is(err, ErrCustomerExists):
		return &respond.APIError{Status: http.StatusConflict, Code: "customer_exists", Message: "a customer with this name already exists"}
	case errors.Is(err, ErrFlowNotAssigned):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_assigned", Message: "this flow is not assigned to the customer"}
	case errors.Is(err, ErrCustomerPaused):
		return &respond.APIError{Status: http.StatusConflict, Code: "customer_paused", Message: "proofing is paused for this customer"}
	case errors.Is(err, ErrCustomerHasOpenReviews):
		return &respond.APIError{Status: http.StatusConflict, Code: "customer_has_open_reviews", Message: "decide this customer's sessions waiting for review first"}
	case errors.Is(err, ErrCustomerSessionsLeft):
		return &respond.APIError{Status: http.StatusConflict, Code: "customer_sessions_left", Message: "sessions were sent for this customer while it was being removed; try again"}
	case errors.Is(err, ErrCustomerNoAPIKey):
		return &respond.APIError{Status: http.StatusConflict, Code: "customer_no_api_key", Message: "create a live API key for this customer first"}
	case errors.Is(err, ErrAPIKeyNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "api_key_not_found", Message: "this API key does not exist"}
	case errors.Is(err, ErrRequestNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "this session does not exist"}
	case errors.Is(err, ErrWebhookNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "webhook_not_found", Message: "this customer has no webhook endpoint"}
	case errors.Is(err, ErrNoCustomerLogo):
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
	case errors.Is(err, ErrReferencePhotoRequired):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "reference_photo_required", Message: "this flow matches the face against your own photo of the person: send it as referencePhoto through the customer API"}
	case errors.Is(err, ErrDiplomasNeedPage):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "diplomas_need_page", Message: "this flow asks for diplomas, which are uploaded on a page: show the session on screen or send a hosted link"}
	case errors.Is(err, ErrHostedDisabled):
		return &respond.APIError{Status: http.StatusConflict, Code: "hosted_disabled", Message: "this flow's hosted page is switched off"}
	case errors.Is(err, ErrProofingPaused):
		return &respond.APIError{Status: http.StatusForbidden, Code: "proofing_paused", Message: "identity proofing is paused for this organisation"}
	case errors.Is(err, ErrOrgNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "organization_not_found", Message: "organisation not found"}
	case errors.Is(err, ErrFlowNoIdentity):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_no_identity", Message: "a data request flow must read the name and date of birth, which a person's sessions are found by"}
	case errors.Is(err, ErrDataFlowForMember):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "data_flow_for_member", Message: "a data request flow is for a customer's subjects, not for members"}
	case errors.Is(err, ErrExportUnavailable):
		return &respond.APIError{Status: http.StatusGone, Code: "export_unavailable", Message: "no data is available to download for this session"}
	case errors.Is(err, ErrRedirectNotAllowed):
		return &respond.APIError{Status: http.StatusBadRequest, Code: "redirect_not_allowed", Message: "redirectUrl must be on one of the customer's allowed redirect origins"}
	case errors.Is(err, proofingprovider.ErrMethodUnavailable):
		return &respond.APIError{Status: http.StatusConflict, Code: "method_unavailable", Message: "Yivi app sessions need the Regula face check, which this deployment has not configured"}
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
