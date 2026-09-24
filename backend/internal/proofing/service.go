package proofing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
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
	Create(ctx context.Context, in NewStoredRequest) (Request, error)
	AttachSession(ctx context.Context, req Request, sess proofingprovider.Session) (bool, error)
	List(ctx context.Context, orgID uuid.UUID, filter RequestFilter) ([]Request, error)
	MarkStarted(ctx context.Context, req Request, sessionID string) error
	EndSession(ctx context.Context, req Request, sessionID string, ipsStatus proofingprovider.Status) error
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
}

type customerStore interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Customer, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Customer, error)
	Create(ctx context.Context, orgID, createdBy uuid.UUID, name string) (Customer, error)
	Rename(ctx context.Context, orgID, id uuid.UUID, name string) (Customer, error)
	SaveFlows(ctx context.Context, orgID, id uuid.UUID, sel FlowSelection) (Customer, error)
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service):
// deepLink is the session's vcmrtd link, validFor how long the session runs.
type Mailer interface {
	SendIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, to, orgName, requesterName, deepLink string, validFor time.Duration) error
}

// Service orchestrates IPS, the settings, request and customer stores, and the mailer.
type Service struct {
	settings  settingsStore
	requests  requestStore
	customers customerStore
	ips       provider
	mailer    Mailer
	now       func() time.Time
}

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
func NewService(settings settingsStore, requests requestStore, customers customerStore, ips provider, mailer Mailer) *Service {
	return &Service{
		settings: settings, requests: requests, customers: customers, ips: ips, mailer: mailer, now: time.Now,
	}
}

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
	sel, err := s.validSelection(ctx, org, sel)
	if err != nil {
		return err
	}
	return s.settings.SaveFlowSelection(ctx, org.ID, sel)
}

// validSelection deduplicates sel and checks it: every flow an active IPS flow
// of the org a recipient can finish, and a non-empty selection has its default
// among it. Both the members' allow-list and a customer's assignment are one.
func (s *Service) validSelection(ctx context.Context, org Org, sel FlowSelection) (FlowSelection, error) {
	ids := make([]string, 0, len(sel.FlowIDs))
	for _, id := range sel.FlowIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sel.FlowIDs = ids
	if len(ids) == 0 && sel.DefaultFlowID != "" {
		return sel, fmt.Errorf("%w: the default flow must be one of the available flows", ErrInvalidInput)
	}
	if len(ids) > 0 && !slices.Contains(ids, sel.DefaultFlowID) {
		return sel, fmt.Errorf("%w: choose which of the available flows is the default", ErrInvalidInput)
	}
	flows, err := s.Flows(ctx, org, true)
	if err != nil {
		return sel, err
	}
	for _, id := range ids {
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == id })
		if i < 0 {
			return sel, ErrFlowNotFound
		}
		if !Completable(flows[i].Flow) {
			return sel, ErrFlowNotCompletable
		}
	}
	return sel, nil
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

// Requester is the member sending a request.
type Requester struct {
	UserID uuid.UUID
	Name   string
}

// Sent is a stored request and whether its e-mail went out. A request whose mail
// failed still stands (and is audited), but the sender needs to know the
// recipient never got the link, so the failure is reported rather than only
// logged.
type Sent struct {
	Request  Request
	MailSent bool
}

// CreateRequest sends a proofing request: to a member, on one of the flows the
// org admin made available to members, or for a customer to its subject, on one
// of the flows assigned to that customer. It creates the request's IPS session,
// stores the request with it, and mails the subject the session's vcmrtd deep
// link: SessionTTL runs from the send. A session whose request then fails to
// store lapses unused at IPS.
func (s *Service) CreateRequest(ctx context.Context, org Org, by Requester, in NewRequest) (Sent, error) {
	subject, err := s.subject(ctx, org, in)
	if err != nil {
		return Sent{}, err
	}
	flow, err := s.sendableFlow(ctx, org, in)
	if err != nil {
		return Sent{}, err
	}
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return Sent{}, err
	}

	id := uuid.New()
	sess, err := s.ips.CreateSession(ctx, apiKey, proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: id.String(), TTL: SessionTTL,
	})
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: %w", id, err)
	}
	if sess.Claim == nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: IPS offered no vcmrtd link", id)
	}
	req, err := s.requests.Create(ctx, NewStoredRequest{
		ID: id, OrgID: org.ID, RequestedBy: by.UserID, Subject: subject,
		Flow: flow, LinkExpiresAt: sess.ExpiresAt,
	})
	if err != nil {
		return Sent{}, err
	}
	attached, err := s.requests.AttachSession(ctx, req, sess)
	if err != nil {
		return Sent{}, err
	}
	if !attached {
		return Sent{}, fmt.Errorf("proofing: attach session request %s: the new request moved on", id)
	}
	if sess.FlowVersion != 0 {
		req.FlowVersion = sess.FlowVersion
	}
	req.session = &ipsSession{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt}

	out := Sent{Request: req}
	if s.mailer != nil {
		err := s.mailer.SendIdentityProofingRequested(ctx, org.ID, subject.Email, org.Name, by.Name, sess.Claim.DeepLink, SessionTTL)
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: request e-mail not sent",
				slog.String("org_id", org.ID.String()), slog.String("request_id", req.ID.String()), slog.Any("error", err))
		}
		out.MailSent = err == nil
	}
	return out, nil
}

// subject resolves who a new request goes to: the member, or the customer's
// subject by the address and optional name the sender gave.
func (s *Service) subject(ctx context.Context, org Org, in NewRequest) (Subject, error) {
	if in.CustomerID == nil {
		m, err := s.requests.Member(ctx, org.ID, in.SubjectUserID)
		if err != nil {
			return Subject{}, err
		}
		return Subject{UserID: &m.UserID, Name: m.Name, Email: m.Email}, nil
	}
	if in.SubjectUserID != uuid.Nil {
		return Subject{}, fmt.Errorf("%w: a request goes to a member or to a customer's subject, not both", ErrInvalidInput)
	}
	if _, err := s.customers.Get(ctx, org.ID, *in.CustomerID); err != nil {
		return Subject{}, err
	}
	email, err := user.ParseEmail(in.SubjectEmail)
	if err != nil {
		return Subject{}, fmt.Errorf("%w: enter a valid e-mail address", ErrInvalidInput)
	}
	name := strings.TrimSpace(in.SubjectName)
	if len(name) > maxSubjectNameLength {
		return Subject{}, fmt.Errorf("%w: the name is too long", ErrInvalidInput)
	}
	return Subject{CustomerID: in.CustomerID, Name: name, Email: string(email)}, nil
}

// sendableFlow is the flow a new request runs, if the sender may use it: for a
// member, a flow the admin made available to members; for a customer's subject,
// one assigned to that customer. Either way it must be one a recipient can finish.
func (s *Service) sendableFlow(ctx context.Context, org Org, in NewRequest) (proofingprovider.Flow, error) {
	flows, err := s.Flows(ctx, org, true)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == in.FlowID })
	switch {
	case i < 0:
		return proofingprovider.Flow{}, ErrFlowNotFound
	case !Completable(flows[i].Flow):
		return proofingprovider.Flow{}, ErrFlowNotCompletable
	}
	if in.CustomerID == nil {
		if !flows[i].Allowed {
			return proofingprovider.Flow{}, ErrFlowNotAllowed
		}
		return flows[i].Flow, nil
	}
	customer, err := s.customers.Get(ctx, org.ID, *in.CustomerID)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if !slices.Contains(customer.Flows.FlowIDs, in.FlowID) {
		return proofingprovider.Flow{}, ErrFlowNotAssigned
	}
	return flows[i].Flow, nil
}

// Requests lists the org's requests narrowed by filter, re-checking a bounded
// number of live ones at IPS first: this read is how an outcome lands here.
func (s *Service) Requests(ctx context.Context, orgID uuid.UUID, filter RequestFilter) ([]Request, error) {
	reqs, err := s.requests.List(ctx, orgID, filter)
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
		// The session ended undecided, and with it the request: a new one means
		// a new mail.
		if err := s.requests.EndSession(ctx, req, sess.ID, res.Status); err != nil {
			slog.WarnContext(ctx, "identity proofing: end session failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req
		}
		ended, now := *sess, s.now()
		ended.EndedAt = &now
		req.session = &ended
		return req
	default:
		return req
	}
	if next == req.Status {
		return req
	}
	// The name read off the document is kept only for a customer's subject the
	// sender may know only by address, and only once the document is approved.
	if req.CustomerID == nil || next != StatusApproved {
		res.Name = ""
	}
	if err := s.requests.RecordOutcome(ctx, req, sess.ID, next, res); err != nil {
		slog.WarnContext(ctx, "identity proofing: record outcome failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req
	}
	req.Status, req.AssuranceLevel, req.EIDASLevel, req.ErrorCode = next, res.AssuranceLevel, res.EIDASLevel, res.ErrorCode
	req.ProofedName = res.Name
	completedAt := s.now()
	if res.CompletedAt != nil {
		completedAt = *res.CompletedAt
	}
	req.CompletedAt = &completedAt
	return req
}

// Customers lists the org's customers, each with its assigned flows.
func (s *Service) Customers(ctx context.Context, orgID uuid.UUID) ([]Customer, error) {
	return s.customers.List(ctx, orgID)
}

// Customer returns one of the org's customers.
func (s *Service) Customer(ctx context.Context, orgID, id uuid.UUID) (Customer, error) {
	return s.customers.Get(ctx, orgID, id)
}

// CustomerFlows is the org's flows with the customer's assignment applied: every
// flow (the admin's view, to assign from) or only the assigned ones a recipient
// can finish (what a member may send on for the customer).
func (s *Service) CustomerFlows(ctx context.Context, org Org, id uuid.UUID, all bool) ([]CustomerFlow, error) {
	customer, err := s.customers.Get(ctx, org.ID, id)
	if err != nil {
		return nil, err
	}
	flows, err := s.Flows(ctx, org, true)
	if err != nil {
		return nil, err
	}
	out := make([]CustomerFlow, 0, len(flows))
	for _, f := range flows {
		cf := CustomerFlow{
			Flow:     f.Flow,
			Assigned: slices.Contains(customer.Flows.FlowIDs, f.ID) && Completable(f.Flow),
			Default:  f.ID == customer.Flows.DefaultFlowID,
		}
		if all || cf.Assigned {
			out = append(out, cf)
		}
	}
	return out, nil
}

// CreateCustomer adds a customer to the org, with no flows assigned yet.
func (s *Service) CreateCustomer(ctx context.Context, orgID, createdBy uuid.UUID, name string) (Customer, error) {
	name, err := customerName(name)
	if err != nil {
		return Customer{}, err
	}
	return s.customers.Create(ctx, orgID, createdBy, name)
}

// RenameCustomer changes a customer's name.
func (s *Service) RenameCustomer(ctx context.Context, orgID, id uuid.UUID, name string) (Customer, error) {
	name, err := customerName(name)
	if err != nil {
		return Customer{}, err
	}
	return s.customers.Rename(ctx, orgID, id, name)
}

// AssignCustomerFlows replaces the flows assigned to a customer. Any flow of the
// org a recipient can finish may be assigned, whether or not members may use it.
func (s *Service) AssignCustomerFlows(ctx context.Context, org Org, id uuid.UUID, sel FlowSelection) (Customer, error) {
	if _, err := s.customers.Get(ctx, org.ID, id); err != nil {
		return Customer{}, err
	}
	sel, err := s.validSelection(ctx, org, sel)
	if err != nil {
		return Customer{}, err
	}
	return s.customers.SaveFlows(ctx, org.ID, id, sel)
}

func customerName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len(name) > maxCustomerNameLength {
		return "", fmt.Errorf("%w: a customer needs a name of at most %d characters", ErrInvalidInput, maxCustomerNameLength)
	}
	return name, nil
}
