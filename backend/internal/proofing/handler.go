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

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// Handler serves identity proofing: the org routes (any member reads the flows
// made available to them and sends requests; defining flows and choosing which
// members may use is admin-only) and the public routes a recipient's proofing
// link opens. The org's IPS tenant is provisioned by whichever route uses it first.
type Handler struct {
	service     *Service
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(service *Service, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler { return h.requireUser(h.authorize(next)) }
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows", member(respond.HandlerFunc(h.listFlows)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows", admin(respond.HandlerFunc(h.createFlow)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.listFlowVersions)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions", admin(respond.HandlerFunc(h.editFlow)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/flows/{flowID}/versions/{version}/activate", admin(respond.HandlerFunc(h.activateFlowVersion)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/flow-selection", admin(respond.HandlerFunc(h.configureFlows)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/members", member(respond.HandlerFunc(h.listMembers)))
	mux.Handle("GET /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.listRequests)))
	mux.Handle("POST /orgs/{slug}/identity-proofing/requests", member(respond.HandlerFunc(h.createRequest)))

	// The recipient's link: a bearer token, like /reidentify/{token}.
	mux.Handle("GET /identity-proofing/{token}", respond.HandlerFunc(h.linkPreview))
	mux.Handle("POST /identity-proofing/{token}/session", respond.HandlerFunc(h.startSession))
}

func orgFromRequest(r *http.Request) Org {
	org := organization.OrgFromContext(r.Context())
	return Org{ID: org.ID, Name: org.Name}
}

type flowResponse struct {
	proofingprovider.Flow
	// Completable is false for a flow a recipient cannot finish with only the
	// vcmrtd app (see Completable); it cannot be made available to members.
	Completable bool `json:"completable"`
	// Allowed is the admin having made the flow available to members; Default
	// is the one the request form preselects.
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
}

func newFlowResponse(f OrgFlow) flowResponse {
	if f.Steps == nil {
		f.Steps = []string{}
	}
	return flowResponse{Flow: f.Flow, Completable: Completable(f.Flow), Allowed: f.Allowed, Default: f.Default}
}

// listFlows shows an admin every flow of the org and a member the ones the admin
// made available to them.
func (h *Handler) listFlows(w http.ResponseWriter, r *http.Request) error {
	flows, err := h.service.Flows(r.Context(), orgFromRequest(r), organization.IsAdmin(r.Context()))
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

type memberResponse struct {
	UserID               uuid.UUID `json:"userId"`
	Name                 string    `json:"name"`
	Email                string    `json:"email"`
	Role                 string    `json:"role"`
	MemberType           string    `json:"memberType"`
	ExternalOrganisation string    `json:"externalOrganisation,omitempty"`
}

// listMembers is everyone of the org a request can be sent to.
func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) error {
	members, err := h.service.Members(r.Context(), orgFromRequest(r).ID)
	if err != nil {
		return mapError(err)
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, memberResponse(m))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type requestResponse struct {
	ID              uuid.UUID  `json:"id"`
	RequestedByName string     `json:"requestedByName"`
	SubjectUserID   *uuid.UUID `json:"subjectUserId,omitempty"`
	SubjectName     string     `json:"subjectName"`
	SubjectEmail    string     `json:"subjectEmail"`
	FlowID          string     `json:"flowId"`
	FlowName        string     `json:"flowName"`
	FlowVersion     int        `json:"flowVersion,omitempty"`
	Status          Status     `json:"status"`
	AssuranceLevel  string     `json:"assuranceLevel,omitempty"`
	EIDASLevel      string     `json:"eidasLevel,omitempty"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	LinkExpiresAt   time.Time  `json:"linkExpiresAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

func newRequestResponse(req Request, now time.Time) requestResponse {
	return requestResponse{
		ID: req.ID, RequestedByName: req.RequestedByName, SubjectUserID: req.SubjectUserID,
		SubjectName: req.SubjectName, SubjectEmail: req.SubjectEmail,
		FlowID: req.FlowID, FlowName: req.FlowName, FlowVersion: req.FlowVersion, Status: req.EffectiveStatus(now),
		AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel, ErrorCode: req.ErrorCode,
		LinkExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
	}
}

// listRequests shows an admin every request of the org and a member the ones they sent.
func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var requestedBy *uuid.UUID
	if !organization.IsAdmin(r.Context()) {
		id := auth.UserFromContext(r.Context()).ID
		requestedBy = &id
	}
	reqs, err := h.service.Requests(r.Context(), org.ID, requestedBy)
	if err != nil {
		return mapError(err)
	}
	now := time.Now()
	out := make([]requestResponse, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, newRequestResponse(req, now))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type createRequestRequest struct {
	UserID uuid.UUID `json:"userId"`
	FlowID string    `json:"flowId"`
}

type createRequestResponse struct {
	requestResponse
	// MailSent is false when the org's mail could not be sent; the request
	// stands, but the member never got its short-lived link.
	MailSent bool `json:"mailSent"`
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) error {
	var body createRequestRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	sent, err := h.service.CreateRequest(r.Context(), orgFromRequest(r),
		Requester{UserID: caller.ID, Name: displayName(caller)}, NewRequest{SubjectUserID: body.UserID, FlowID: body.FlowID})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, createRequestResponse{
		requestResponse: newRequestResponse(sent.Request, time.Now()), MailSent: sent.MailSent,
	})
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

type linkResponse struct {
	OrganizationName string    `json:"organizationName"`
	SubjectName      string    `json:"subjectName"`
	FlowName         string    `json:"flowName"`
	Status           Status    `json:"status"`
	LinkExpiresAt    time.Time `json:"linkExpiresAt"`
}

func (h *Handler) linkPreview(w http.ResponseWriter, r *http.Request) error {
	link, err := h.service.Link(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, linkResponse{
		OrganizationName: link.OrganizationName, SubjectName: link.Request.SubjectName,
		FlowName: link.Request.FlowName, Status: link.Request.EffectiveStatus(time.Now()),
		LinkExpiresAt: link.Request.LinkExpiresAt,
	})
	return nil
}

type startResponse struct {
	Status         Status     `json:"status"`
	DeepLink       string     `json:"deepLink,omitempty"`
	ClaimExpiresAt *time.Time `json:"claimExpiresAt,omitempty"`
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) error {
	res, err := h.service.Start(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, startResponse(res))
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
	case errors.Is(err, ErrLinkNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "proofing_link_not_found", Message: "this identity verification link is invalid or has expired"}
	case errors.Is(err, ErrFlowNotFound):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_found", Message: "the selected flow does not exist"}
	case errors.Is(err, ErrFlowNotCompletable):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_completable", Message: "this flow cannot be finished from the wallet: its face step needs the NFC chip read to compare against and must run in the app"}
	case errors.Is(err, ErrMemberNotFound):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "member_not_found", Message: "that person is not a member of this organization"}
	case errors.Is(err, ErrFlowNotAllowed):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "flow_not_allowed", Message: "your organization's admin has not made this flow available"}
	case errors.As(err, &rejected):
		// IPS's own validation message (e.g. which check a step requires) is what
		// the admin needs to fix the flow.
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "rejected_by_provider", Message: rejected.Message}
	default:
		return fmt.Errorf("identity proofing: %w", err)
	}
}
