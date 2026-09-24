package proofing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// provider is the IPS surface the service drives (implemented by
// *proofingprovider.Client and *proofingprovider.Stub).
type provider interface {
	CreateTenant(ctx context.Context, name string) (proofingprovider.Tenant, error)
	CreateAPIKey(ctx context.Context, tenantID string, scopes []string) (string, error)
	ListFlows(ctx context.Context, apiKey string) ([]proofingprovider.Flow, error)
	CreateFlow(ctx context.Context, apiKey string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	CreateFlowVersion(ctx context.Context, apiKey, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	ListFlowVersions(ctx context.Context, apiKey, id string) ([]proofingprovider.Flow, error)
	ActivateFlowVersion(ctx context.Context, apiKey, id string, version int) (proofingprovider.Flow, error)
	CreateSession(ctx context.Context, apiKey string, in proofingprovider.SessionInput) (proofingprovider.Session, error)
	MintClaim(ctx context.Context, apiKey, sessionID, sessionToken string) (*proofingprovider.Claim, error)
	SessionResult(ctx context.Context, apiKey, sessionID, sessionToken string) (proofingprovider.Result, error)
}

type settingsStore interface {
	CanStoreSecrets() bool
	APIKey(ctx context.Context, orgID uuid.UUID) (string, error)
	Save(ctx context.Context, orgID uuid.UUID, tenantID, apiKey, webhookSecret string) (bool, error)
	FlowSelection(ctx context.Context, orgID uuid.UUID) (FlowSelection, error)
	SaveFlowSelection(ctx context.Context, orgID uuid.UUID, sel FlowSelection) error
	RecordFlowEvent(ctx context.Context, orgID uuid.UUID, action string, flow proofingprovider.Flow) error
}

type requestStore interface {
	Create(ctx context.Context, in NewStoredRequest) (Request, string, error)
	List(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]Request, error)
	ByToken(ctx context.Context, rawToken string) (Link, error)
	MarkStarted(ctx context.Context, req Request, sessionID string) error
	ExpireLink(ctx context.Context, id uuid.UUID, sessionID string) error
	Members(ctx context.Context, orgID uuid.UUID) ([]Member, error)
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service).
type Mailer interface {
	SendIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, to, orgName, requesterName, proofingURL string, validFor time.Duration) error
}

// Service orchestrates IPS, the settings and request stores, and the mailer.
type Service struct {
	settings   settingsStore
	requests   requestStore
	ips        provider
	mailer     Mailer
	appBaseURL string
	now        func() time.Time
}

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
func NewService(settings settingsStore, requests requestStore, ips provider, mailer Mailer, appBaseURL string) *Service {
	return &Service{
		settings: settings, requests: requests, ips: ips, mailer: mailer,
		appBaseURL: strings.TrimRight(appBaseURL, "/"), now: time.Now,
	}
}

// ProofPath is the public page a proofing link opens.
func ProofPath(rawToken string) string { return "/proof/" + url.PathEscape(rawToken) }

// orgAPIKey returns the org's IPS API key, provisioning the org's own IPS tenant
// and key on its first use: an org needs no enable step before it can proof.
func (s *Service) orgAPIKey(ctx context.Context, org Org) (string, error) {
	apiKey, err := s.settings.APIKey(ctx, org.ID)
	if !errors.Is(err, ErrNotProvisioned) {
		return apiKey, err
	}
	if err := s.provision(ctx, org); err != nil {
		return "", err
	}
	return s.settings.APIKey(ctx, org.ID)
}

// provision creates the org's IPS tenant and a scoped API key and stores them.
// The encryption key is checked first, so a deployment without one never leaves
// an orphaned tenant at IPS. Concurrent first uses may each create a tenant; one
// is stored and the others are left unused at IPS (logged, not prevented).
func (s *Service) provision(ctx context.Context, org Org) error {
	if !s.settings.CanStoreSecrets() {
		return ErrNoEncryptionKey
	}
	tenant, err := s.ips.CreateTenant(ctx, org.Name)
	if err != nil {
		return fmt.Errorf("proofing: provision tenant org %s: %w", org.ID, err)
	}
	apiKey, err := s.ips.CreateAPIKey(ctx, tenant.ID, proofingprovider.TenantKeyScopes)
	if err != nil {
		return fmt.Errorf("proofing: provision api key org %s: %w", org.ID, err)
	}
	saved, err := s.settings.Save(ctx, org.ID, tenant.ID, apiKey, tenant.WebhookSecret)
	if err != nil {
		return err
	}
	if !saved {
		slog.WarnContext(ctx, "identity proofing: concurrent provisioning left an unused IPS tenant",
			slog.String("org_id", org.ID.String()), slog.String("ips_tenant_id", tenant.ID))
	}
	return nil
}

// Flows returns the org's active IPS flows with the admin's selection applied.
// all lists every flow (the admin's view); otherwise only the flows members may
// send a request on.
func (s *Service) Flows(ctx context.Context, org Org, all bool) ([]OrgFlow, error) {
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return nil, err
	}
	flows, err := s.ips.ListFlows(ctx, apiKey)
	if err != nil {
		return nil, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	sel, err := s.settings.FlowSelection(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgFlow, 0, len(flows))
	for _, f := range flows {
		of := OrgFlow{Flow: f, Allowed: slices.Contains(sel.FlowIDs, f.ID) && Completable(f), Default: f.ID == sel.DefaultFlowID}
		if all || of.Allowed {
			out = append(out, of)
		}
	}
	return out, nil
}

// ConfigureFlows replaces the org's allow-list. Every chosen flow must be an
// active IPS flow a recipient can finish, and a non-empty selection needs a
// default among it.
func (s *Service) ConfigureFlows(ctx context.Context, org Org, sel FlowSelection) error {
	ids := make([]string, 0, len(sel.FlowIDs))
	for _, id := range sel.FlowIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sel.FlowIDs = ids
	if len(ids) == 0 && sel.DefaultFlowID != "" {
		return fmt.Errorf("%w: the default flow must be one of the available flows", ErrInvalidInput)
	}
	if len(ids) > 0 && !slices.Contains(ids, sel.DefaultFlowID) {
		return fmt.Errorf("%w: choose which of the available flows is the default", ErrInvalidInput)
	}
	flows, err := s.Flows(ctx, org, true)
	if err != nil {
		return err
	}
	for _, id := range ids {
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == id })
		if i < 0 {
			return ErrFlowNotFound
		}
		if !Completable(flows[i].Flow) {
			return ErrFlowNotCompletable
		}
	}
	return s.settings.SaveFlowSelection(ctx, org.ID, sel)
}

// CreateFlow defines a flow at IPS (version 1) and audits it. IPS validates the
// combination of steps, checks and assurance level; its refusal comes back as a
// *proofingprovider.RejectedError.
func (s *Service) CreateFlow(ctx context.Context, org Org, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	in, err := normalizeFlow(in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	flow, err := s.ips.CreateFlow(ctx, apiKey, in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowCreated, flow); err != nil {
		return proofingprovider.Flow{}, err
	}
	return flow, nil
}

// EditFlow saves in as the next version of flow id. IPS makes it the active
// version at once; requests already sent keep the version their session pinned.
func (s *Service) EditFlow(ctx context.Context, org Org, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	in, err := normalizeFlow(in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	flow, err := s.ips.CreateFlowVersion(ctx, apiKey, id, in)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowVersionCreated, flow); err != nil {
		return proofingprovider.Flow{}, err
	}
	return flow, nil
}

// FlowVersions returns every version of flow id, oldest first.
func (s *Service) FlowVersions(ctx context.Context, org Org, id string) ([]proofingprovider.Flow, error) {
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return nil, err
	}
	versions, err := s.ips.ListFlowVersions(ctx, apiKey, id)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		return nil, ErrFlowNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("proofing: list flow versions org %s: %w", org.ID, err)
	}
	return versions, nil
}

// ActivateFlowVersion makes an earlier (or later) version of flow id the one new
// requests run, and audits it.
func (s *Service) ActivateFlowVersion(ctx context.Context, org Org, id string, version int) (proofingprovider.Flow, error) {
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	flow, err := s.ips.ActivateFlowVersion(ctx, apiKey, id, version)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowVersionActivated, flow); err != nil {
		return proofingprovider.Flow{}, err
	}
	return flow, nil
}

// normalizeFlow checks what the wallet itself requires of a flow and fixes the
// face capture to the app: a recipient only has vcmrtd/idem (IPS has no end-user
// web page yet), while IPS's own default is the browser.
func normalizeFlow(in proofingprovider.FlowSpec) (proofingprovider.FlowSpec, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Steps) == 0 {
		return in, fmt.Errorf("%w: a flow needs a name and at least one step", ErrInvalidInput)
	}
	in.SelfieLocation = ""
	if hasFaceStep(in.Steps) {
		in.SelfieLocation = selfieLocationNative
	}
	return in, nil
}

// Members lists everyone a request can be sent to.
func (s *Service) Members(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	return s.requests.Members(ctx, orgID)
}

// Requester is the member sending a request.
type Requester struct {
	UserID uuid.UUID
	Name   string
}

// Sent is a stored request and whether its e-mail went out. A request whose mail
// failed still stands (and is audited), but with a link this short-lived the
// sender needs to know, so the failure is reported rather than only logged.
type Sent struct {
	Request  Request
	MailSent bool
}

// CreateRequest sends a member a proofing request on one of the flows the org
// admin made available: it creates the IPS session now, stores the request with
// a link that expires with that session, and mails the member the link as a QR
// code and a button.
func (s *Service) CreateRequest(ctx context.Context, org Org, by Requester, in NewRequest) (Sent, error) {
	subject, err := s.requests.Member(ctx, org.ID, in.SubjectUserID)
	if err != nil {
		return Sent{}, err
	}
	flows, err := s.Flows(ctx, org, true)
	if err != nil {
		return Sent{}, err
	}
	i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == in.FlowID })
	switch {
	case i < 0:
		return Sent{}, ErrFlowNotFound
	case !Completable(flows[i].Flow):
		return Sent{}, ErrFlowNotCompletable
	case !flows[i].Allowed:
		return Sent{}, ErrFlowNotAllowed
	}
	flow := flows[i].Flow

	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return Sent{}, err
	}
	id := uuid.New()
	sess, err := s.ips.CreateSession(ctx, apiKey, proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: id.String(), TTL: SessionTTL,
	})
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session org %s: %w", org.ID, err)
	}
	// The link lives exactly as long as the session, never past SessionTTL.
	linkExpiresAt := s.now().Add(SessionTTL)
	if !sess.ExpiresAt.IsZero() && sess.ExpiresAt.Before(linkExpiresAt) {
		linkExpiresAt = sess.ExpiresAt
	}

	req, rawToken, err := s.requests.Create(ctx, NewStoredRequest{
		ID: id, OrgID: org.ID, RequestedBy: by.UserID, Subject: subject,
		Flow: flow, Session: sess, LinkExpiresAt: linkExpiresAt,
	})
	if err != nil {
		return Sent{}, err
	}
	out := Sent{Request: req}
	if s.mailer != nil {
		link := s.appBaseURL + ProofPath(rawToken)
		err := s.mailer.SendIdentityProofingRequested(ctx, org.ID, subject.Email, org.Name, by.Name, link,
			linkExpiresAt.Sub(s.now()).Round(time.Minute))
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: request e-mail not sent",
				slog.String("org_id", org.ID.String()), slog.String("request_id", req.ID.String()), slog.Any("error", err))
		}
		out.MailSent = err == nil
	}
	return out, nil
}

// Requests lists the org's requests (or one member's), re-checking a bounded
// number of live ones at IPS first so a closed recipient page still lands its
// outcome here.
func (s *Service) Requests(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]Request, error) {
	reqs, err := s.requests.List(ctx, orgID, requestedBy)
	if err != nil {
		return nil, err
	}
	var apiKey string
	checked := 0
	for i := range reqs {
		if !reqs[i].needsReconcile() || checked == maxReconcilePerList {
			continue
		}
		if apiKey == "" {
			if apiKey, err = s.settings.APIKey(ctx, orgID); err != nil {
				return nil, err
			}
		}
		checked++
		reqs[i] = s.reconcile(ctx, apiKey, reqs[i])
	}
	return reqs, nil
}

// Link resolves a public proofing link, reconciling a live session first.
func (s *Service) Link(ctx context.Context, rawToken string) (Link, error) {
	link, err := s.requests.ByToken(ctx, rawToken)
	if err != nil || !link.Request.needsReconcile() {
		return link, err
	}
	apiKey, err := s.settings.APIKey(ctx, link.Request.OrganizationID)
	if err != nil {
		return Link{}, err
	}
	link.Request = s.reconcile(ctx, apiKey, link.Request)
	return link, nil
}

// Start gives the recipient their next step: a fresh vcmrtd/idem claim on the
// request's session while it lives. IPS's claim is single-use and shorter-lived
// than the session, so the page asks again whenever the one it shows lapses.
func (s *Service) Start(ctx context.Context, rawToken string) (StartResult, error) {
	link, err := s.Link(ctx, rawToken)
	if err != nil {
		return StartResult{}, err
	}
	req := link.Request
	if req.Status.Settled() {
		return StartResult{Status: req.Status}, nil
	}
	if req.session == nil || !s.now().Before(req.session.ExpiresAt) {
		return StartResult{}, ErrLinkNotFound
	}
	apiKey, err := s.settings.APIKey(ctx, req.OrganizationID)
	if err != nil {
		return StartResult{}, err
	}
	claim, err := s.ips.MintClaim(ctx, apiKey, req.session.ID, req.session.Token)
	if err != nil {
		return StartResult{}, fmt.Errorf("proofing: mint claim request %s: %w", req.ID, err)
	}
	return startResult(StatusInProgress, claim), nil
}

func startResult(status Status, claim *proofingprovider.Claim) StartResult {
	out := StartResult{Status: status}
	if claim != nil {
		out.DeepLink = claim.DeepLink
		expiresAt := claim.ExpiresAt
		out.ClaimExpiresAt = &expiresAt
	}
	return out
}

// reconcile reads the request's IPS session and records what IPS decided. An IPS
// failure is logged and the row is shown as last known: a read must not fail
// because IPS is briefly away.
func (s *Service) reconcile(ctx context.Context, apiKey string, req Request) Request {
	sess := req.session
	res, err := s.ips.SessionResult(ctx, apiKey, sess.ID, sess.Token)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		// IPS purged or erased the session: treat it like a lapsed one.
		res, err = proofingprovider.Result{Status: proofingprovider.StatusExpired}, nil
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: reconcile failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req
	}

	var next Status
	switch res.Status {
	case proofingprovider.StatusApproved:
		next = StatusApproved
	case proofingprovider.StatusRejected:
		next = StatusRejected
	case proofingprovider.StatusNeedsReview:
		next = StatusNeedsReview
	case proofingprovider.StatusOpened, proofingprovider.StatusInProgress:
		if req.Status != StatusPending {
			return req
		}
		if err := s.requests.MarkStarted(ctx, req, sess.ID); err != nil {
			slog.WarnContext(ctx, "identity proofing: mark started failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req
		}
		req.Status = StatusInProgress
		return req
	case proofingprovider.StatusExpired, proofingprovider.StatusCancelled:
		if req.Status.Settled() {
			return req
		}
		// The link and the session share one lifetime: a session IPS ended early
		// ends the link with it.
		if err := s.requests.ExpireLink(ctx, req.ID, sess.ID); err != nil {
			slog.WarnContext(ctx, "identity proofing: expire link failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req
		}
		if now := s.now(); now.Before(req.LinkExpiresAt) {
			req.LinkExpiresAt = now
		}
		return req
	default:
		return req
	}
	if next == req.Status {
		return req
	}
	if err := s.requests.RecordOutcome(ctx, req, sess.ID, next, res); err != nil {
		slog.WarnContext(ctx, "identity proofing: record outcome failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req
	}
	req.Status, req.AssuranceLevel, req.EIDASLevel, req.ErrorCode = next, res.AssuranceLevel, res.EIDASLevel, res.ErrorCode
	completedAt := s.now()
	if res.CompletedAt != nil {
		completedAt = *res.CompletedAt
	}
	req.CompletedAt = &completedAt
	return req
}
