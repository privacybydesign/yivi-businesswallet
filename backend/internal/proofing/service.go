package proofing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
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
	StartYiviDisclosure(ctx context.Context, sessionToken string) (proofingprovider.YiviStart, error)
	YiviDisclosureResult(ctx context.Context, sessionToken string) (proofingprovider.YiviDisclosure, error)
	SubmitFaceFrame(ctx context.Context, sessionToken, image string) (proofingprovider.FaceVerdict, error)
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
	MarkStarted(ctx context.Context, req Request, sessionID string, method proofingprovider.Method) error
	EndSession(ctx context.Context, req Request, sessionID string, ipsStatus proofingprovider.Status, method proofingprovider.Method) error
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
	Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID, since time.Time) ([]StatsRow, error)
	GetForCustomer(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	ListLive(ctx context.Context, limit int) ([]Request, error)
}

// eventReader reads the audit trail (implemented by *audit.Reader).
type eventReader interface {
	ListForTarget(ctx context.Context, orgID uuid.UUID, targetType, targetID string, after *audit.Cursor, limit int) (audit.Page, error)
}

type webhookStore interface {
	Get(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, error)
	Save(ctx context.Context, orgID, customerID uuid.UUID, url string, events []string) (Webhook, string, error)
	RotateSecret(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, string, error)
	Remove(ctx context.Context, orgID, customerID uuid.UUID) error
	SendTest(ctx context.Context, orgID, customerID uuid.UUID) error
	Deliveries(ctx context.Context, orgID, customerID uuid.UUID) ([]Delivery, error)
	Health(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]WebhookHealth, error)
}

type apiKeyStore interface {
	Create(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string) (APIKey, string, error)
	List(ctx context.Context, orgID, customerID uuid.UUID) ([]APIKey, error)
	Revoke(ctx context.Context, orgID, customerID, id uuid.UUID) (APIKey, error)
	Authenticate(ctx context.Context, raw string) (APIKeyCaller, error)
}

type customerStore interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Customer, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Customer, error)
	Create(ctx context.Context, orgID, createdBy uuid.UUID, name string) (Customer, error)
	Rename(ctx context.Context, orgID, id uuid.UUID, name string) (Customer, error)
	SaveFlows(ctx context.Context, orgID, id uuid.UUID, sel FlowSelection) (Customer, error)
	SetPaused(ctx context.Context, orgID, id uuid.UUID, paused bool) (Customer, error)
	SaveSettings(ctx context.Context, orgID, id uuid.UUID, settings CustomerSettings) (Customer, error)
	SaveBranding(ctx context.Context, orgID, id uuid.UUID, b CustomerBranding, logo LogoChange) (Customer, error)
	Logo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error)
	Remove(ctx context.Context, orgID, id uuid.UUID) error
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service).
type Mailer interface {
	SendIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, m email.ProofingMail) error
}

// Service orchestrates IPS, the settings, request and customer stores, and the mailer.
type Service struct {
	settings  settingsStore
	requests  requestStore
	customers customerStore
	apiKeys   apiKeyStore
	webhooks  webhookStore
	events    eventReader
	ips       provider
	mailer    Mailer
	now       func() time.Time
}

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
// Stores are the service's persistence, one store per concern.
type Stores struct {
	Settings  settingsStore
	Requests  requestStore
	Customers customerStore
	APIKeys   apiKeyStore
	Webhooks  webhookStore
	Events    eventReader
}

func NewService(stores Stores, ips provider, mailer Mailer) *Service {
	return &Service{
		settings: stores.Settings, requests: stores.Requests, customers: stores.Customers, apiKeys: stores.APIKeys,
		webhooks: stores.Webhooks, events: stores.Events, ips: ips, mailer: mailer, now: time.Now,
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
	// APIKeyID is set, and UserID nil, for a customer's backend calling the API.
	APIKeyID *uuid.UUID
}

func (r Requester) userID() *uuid.UUID {
	if r.UserID == uuid.Nil {
		return nil
	}
	return &r.UserID
}

// Sent is a stored request and whether its e-mail went out. A request whose mail
// failed still stands (and is audited), but the sender needs to know the
// recipient never got the link, so the failure is reported rather than only
// logged.
type Sent struct {
	Request  Request
	MailSent bool
	// DeepLink is the session's vcmrtd link, for an API caller to show itself.
	DeepLink string
}

// CreateRequest sends a proofing request: to a member, on one of the flows the
// org admin made available to members, or for a customer to its subject, on one
// of the flows assigned to that customer. It creates the request's IPS session
// for the chosen app, stores the request with it, and, for a mailed request,
// mails the subject the session's vcmrtd deep link: SessionTTL runs from the
// send. An on-screen request is created only once the subject accepted what is
// collected, so its session starts then. A session whose request then fails to
// store lapses unused at IPS.
func (s *Service) CreateRequest(ctx context.Context, org Org, by Requester, in NewRequest) (Sent, error) {
	in, err := normalizeRequest(in)
	if err != nil {
		return Sent{}, err
	}
	subject, customer, err := s.subject(ctx, org, in)
	if err != nil {
		return Sent{}, err
	}
	ttl := SessionTTL
	if customer != nil && customer.Settings.SessionTTL != 0 {
		ttl = customer.Settings.SessionTTL
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
		FlowID: flow.ID, ClientReference: id.String(), TTL: ttl, Method: in.Method,
	})
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: %w", id, err)
	}
	// An Idem session is the vcmrtd link; a Yivi one is started from the screen.
	if in.Method == proofingprovider.MethodIdem && sess.Claim == nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: IPS offered no vcmrtd link", id)
	}
	req, err := s.requests.Create(ctx, NewStoredRequest{
		ID: id, OrgID: org.ID, RequestedBy: by.userID(), APIKeyID: by.APIKeyID, Subject: subject,
		Flow: flow, LinkExpiresAt: sess.ExpiresAt, Method: in.Method, Channel: in.Channel,
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
	if sess.Claim == nil {
		return out, nil
	}
	out.DeepLink = sess.Claim.DeepLink
	if s.mailer != nil && !in.SkipMail && in.Channel == ChannelEmail {
		err := s.mailer.SendIdentityProofingRequested(ctx, org.ID, s.proofingMail(ctx, org, customer, subject, by, sess.Claim.DeepLink, ttl))
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: request e-mail not sent",
				slog.String("org_id", org.ID.String()), slog.String("request_id", req.ID.String()), slog.Any("error", err))
		}
		out.MailSent = err == nil
	}
	return out, nil
}

// normalizeRequest fills in the default app and delivery and refuses a
// combination that cannot run: the Yivi app's face check runs in the browser
// that shows its QR, and a mailed subject has no such page.
func normalizeRequest(in NewRequest) (NewRequest, error) {
	if in.Method == "" {
		in.Method = proofingprovider.MethodIdem
	}
	if in.Channel == "" {
		in.Channel = ChannelEmail
	}
	switch {
	case in.Method != proofingprovider.MethodIdem && in.Method != proofingprovider.MethodYivi:
		return NewRequest{}, fmt.Errorf("%w: choose the Idem app or the Yivi app", ErrInvalidInput)
	case in.Channel != ChannelEmail && in.Channel != ChannelOnScreen:
		return NewRequest{}, fmt.Errorf("%w: send the request by e-mail or show it on screen", ErrInvalidInput)
	case in.Method == proofingprovider.MethodYivi && in.Channel != ChannelOnScreen:
		return NewRequest{}, fmt.Errorf("%w: a Yivi app session runs on this screen and cannot be e-mailed", ErrInvalidInput)
	}
	return in, nil
}

// subject resolves who a new request goes to: the member, or the customer's
// subject by the address and optional name the sender gave, with the customer.
// An on-screen request's subject is in front of the sender and needs no address.
func (s *Service) subject(ctx context.Context, org Org, in NewRequest) (Subject, *Customer, error) {
	if in.CustomerID == nil {
		m, err := s.requests.Member(ctx, org.ID, in.SubjectUserID)
		if err != nil {
			return Subject{}, nil, err
		}
		return Subject{UserID: &m.UserID, Name: m.Name, Email: m.Email}, nil, nil
	}
	if in.SubjectUserID != uuid.Nil {
		return Subject{}, nil, fmt.Errorf("%w: a request goes to a member or to a customer's subject, not both", ErrInvalidInput)
	}
	customer, err := s.customers.Get(ctx, org.ID, *in.CustomerID)
	if err != nil {
		return Subject{}, nil, err
	}
	if customer.Paused() {
		return Subject{}, nil, ErrCustomerPaused
	}
	name := strings.TrimSpace(in.SubjectName)
	if len(name) > maxSubjectNameLength {
		return Subject{}, nil, fmt.Errorf("%w: the name is too long", ErrInvalidInput)
	}
	if in.Channel == ChannelOnScreen && strings.TrimSpace(in.SubjectEmail) == "" {
		return Subject{CustomerID: in.CustomerID, Name: name}, &customer, nil
	}
	email, err := user.ParseEmail(in.SubjectEmail)
	if err != nil {
		return Subject{}, nil, fmt.Errorf("%w: enter a valid e-mail address", ErrInvalidInput)
	}
	return Subject{CustomerID: in.CustomerID, Name: name, Email: string(email)}, &customer, nil
}

// proofingMail is the mail a request sends. A member's is the org's; a
// customer's subject's is signed with and styled as the customer, with its
// support contact and privacy statement. A logo that cannot be read is left
// out (the wordmark shows): a cosmetic loss must not block the send.
func (s *Service) proofingMail(ctx context.Context, org Org, customer *Customer, subject Subject, by Requester,
	deepLink string, ttl time.Duration,
) email.ProofingMail {
	m := email.ProofingMail{
		To: subject.Email, OrgName: org.Name, RequesterName: by.Name, DeepLink: deepLink, ValidFor: ttl,
	}
	if customer == nil {
		return m
	}
	m.OrgName, m.RequesterName = customer.SignedAs(), customer.SignedAs()
	m.SupportContact, m.PrivacyURL = customer.Branding.SupportContact, customer.Branding.PrivacyURL
	m.Brand = &email.CustomerBrand{PrimaryColor: customer.Branding.PrimaryColor}
	if customer.Branding.HasLogo {
		logo, err := s.customers.Logo(ctx, org.ID, customer.ID)
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: customer logo not read, mailing the wordmark",
				slog.String("customer_id", customer.ID.String()), slog.Any("error", err))
		} else {
			m.Brand.Logo = email.Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}
		}
	}
	return m
}

// sendableFlow is the flow a new request runs, if the sender may use it: for a
// member, a flow the admin made available to members; for a customer's subject,
// one assigned to that customer. Either way it must be one a recipient can finish.
func (s *Service) sendableFlow(ctx context.Context, org Org, in NewRequest) (proofingprovider.Flow, error) {
	if in.FlowID == "" && in.CustomerID != nil {
		// An API caller that names no flow gets the customer's default.
		customer, err := s.customers.Get(ctx, org.ID, *in.CustomerID)
		if err != nil {
			return proofingprovider.Flow{}, err
		}
		if in.FlowID = customer.Flows.DefaultFlowID; in.FlowID == "" {
			return proofingprovider.Flow{}, ErrFlowNotAssigned
		}
	}
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

// reconcile reads the request's IPS session and records what IPS decided,
// with no actor: the audit trail shows it as the system's. An IPS
// failure is logged and the row is shown as last known: a read must not fail
// because IPS is briefly away.
func (s *Service) reconcile(ctx context.Context, apiKey string, req Request) Request {
	// What IPS reports is the subject's doing and the wallet's record of it, not
	// that of whoever's read happened to trigger the check.
	ctx = audit.WithoutActor(ctx)
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
		if err := s.requests.MarkStarted(ctx, req, sess.ID, res.Method); err != nil {
			slog.WarnContext(ctx, "identity proofing: mark started failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req
		}
		req.Status = StatusInProgress
		req.Method = methodOr(res.Method, req.Method)
		return req
	case proofingprovider.StatusExpired, proofingprovider.StatusCancelled:
		if req.Status.Settled() {
			return req
		}
		// The session ended undecided, and with it the request: a new one means
		// a new mail.
		if err := s.requests.EndSession(ctx, req, sess.ID, res.Status, res.Method); err != nil {
			slog.WarnContext(ctx, "identity proofing: end session failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req
		}
		ended, now := *sess, s.now()
		ended.EndedAt = &now
		req.session = &ended
		req.Method = methodOr(res.Method, req.Method)
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
	req.Method = methodOr(res.Method, req.Method)
	completedAt := s.now()
	if res.CompletedAt != nil {
		completedAt = *res.CompletedAt
	}
	req.CompletedAt = &completedAt
	return req
}

// methodOr is the method IPS reported, or the one already known: a later read
// that no longer lists the devices does not forget it.
func methodOr(reported, known proofingprovider.Method) proofingprovider.Method {
	if reported != "" {
		return reported
	}
	return known
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

// SetCustomerPaused pauses or resumes proofing for a customer: while paused, no
// request can be sent for it.
func (s *Service) SetCustomerPaused(ctx context.Context, orgID, id uuid.UUID, paused bool) (Customer, error) {
	return s.customers.SetPaused(ctx, orgID, id, paused)
}

// CustomerRequest is one of a customer's requests, re-checked at IPS first
// while IPS may hold a newer state: an API caller polling it is how its outcome
// lands.
func (s *Service) CustomerRequest(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error) {
	req, err := s.requests.GetForCustomer(ctx, orgID, customerID, id)
	if err != nil || !req.needsReconcile() {
		return req, err
	}
	apiKey, err := s.settings.APIKey(ctx, orgID)
	if err != nil {
		return Request{}, err
	}
	return s.reconcile(ctx, apiKey, req), nil
}

// ReconcileLive re-checks live requests at IPS, across every org, so an
// outcome or an expiry (and the webhook it sends) lands without anyone reading
// a list. It reports how many it re-checked; an org whose key cannot be read is
// logged and skipped.
func (s *Service) ReconcileLive(ctx context.Context) (int64, error) {
	reqs, err := s.requests.ListLive(ctx, maxReconcilePerRound)
	if err != nil {
		return 0, err
	}
	keys := map[uuid.UUID]string{}
	var checked int64
	for _, req := range reqs {
		apiKey, ok := keys[req.OrganizationID]
		if !ok {
			if apiKey, err = s.settings.APIKey(ctx, req.OrganizationID); err != nil {
				slog.WarnContext(ctx, "identity proofing: reconcile skipped an org",
					slog.String("org_id", req.OrganizationID.String()), slog.Any("error", err))
			}
			keys[req.OrganizationID] = apiKey
		}
		if apiKey == "" {
			continue
		}
		s.reconcile(ctx, apiKey, req)
		checked++
	}
	return checked, nil
}

// Request is one of the org's requests, re-checked at IPS while it may have
// moved on: what an on-screen session's page polls. A member sees only a
// request they sent (requestedBy set), as in the list.
func (s *Service) Request(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (Request, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil || !req.needsReconcile() {
		return req, err
	}
	apiKey, err := s.settings.APIKey(ctx, orgID)
	if err != nil {
		return Request{}, err
	}
	return s.reconcile(ctx, apiKey, req), nil
}

// sentRequest is one of the org's requests, scoped like the list: an admin
// (requestedBy nil) any, a member only one they sent.
func (s *Service) sentRequest(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (Request, error) {
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if requestedBy != nil && (req.RequestedBy == nil || *req.RequestedBy != *requestedBy) {
		return Request{}, ErrRequestNotFound
	}
	return req, nil
}

// yiviSession is the running IPS session of a request created for the Yivi app.
func (s *Service) yiviSession(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (Request, *ipsSession, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return Request{}, nil, err
	}
	if req.Method != proofingprovider.MethodYivi {
		return Request{}, nil, ErrWrongMethod
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return Request{}, nil, ErrSessionOver
	}
	return req, sess, nil
}

// StartYivi starts (or, after a cancel in the app, restarts) the Yivi
// disclosure of an on-screen Yivi request: its session pointer is the QR the
// subject scans with the Yivi app.
func (s *Service) StartYivi(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (proofingprovider.YiviStart, error) {
	_, sess, err := s.yiviSession(ctx, orgID, id, requestedBy)
	if err != nil {
		return proofingprovider.YiviStart{}, err
	}
	started, err := s.ips.StartYiviDisclosure(ctx, sess.Token)
	return started, yiviStepError(err)
}

// YiviDisclosure redeems the subject's finished Yivi disclosure, or answers
// proofingprovider.ErrDisclosurePending while it is not done. A disclosure
// that ended the session (cancelled, or no usable photo) is recorded at once.
func (s *Service) YiviDisclosure(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (proofingprovider.YiviDisclosure, error) {
	req, sess, err := s.yiviSession(ctx, orgID, id, requestedBy)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	disclosure, err := s.ips.YiviDisclosureResult(ctx, sess.Token)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, yiviStepError(err)
	}
	s.reconcileNow(ctx, req)
	return disclosure, nil
}

// FaceFrame scores one live camera frame of an on-screen Yivi request against
// the disclosed photo. The frame passes through to IPS and is not kept; a
// decision is recorded at once rather than on the next list read.
func (s *Service) FaceFrame(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, image string) (proofingprovider.FaceVerdict, error) {
	req, sess, err := s.yiviSession(ctx, orgID, id, requestedBy)
	if err != nil {
		return proofingprovider.FaceVerdict{}, err
	}
	verdict, err := s.ips.SubmitFaceFrame(ctx, sess.Token, image)
	if err != nil {
		return proofingprovider.FaceVerdict{}, yiviStepError(err)
	}
	if verdict.Decision != proofingprovider.FaceDecisionPending {
		s.reconcileNow(ctx, req)
	}
	return verdict, nil
}

// yiviStepError reads IPS no longer knowing the session (purged, or the stub
// restarted) or answering it gone as the session being over; the request's
// row catches up on the next reconcile.
func yiviStepError(err error) error {
	var rejected *proofingprovider.RejectedError
	if errors.Is(err, proofingprovider.ErrNotFound) || (errors.As(err, &rejected) && rejected.Status == http.StatusGone) {
		return fmt.Errorf("%w: %w", ErrSessionOver, err)
	}
	return err
}

// reconcileNow records what IPS decided for req. A failure is logged: the
// background reconciler picks the outcome up later.
func (s *Service) reconcileNow(ctx context.Context, req Request) {
	apiKey, err := s.settings.APIKey(ctx, req.OrganizationID)
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: reconcile after the Yivi step skipped",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return
	}
	s.reconcile(ctx, apiKey, req)
}

// RequestEvents is one request's audit trail, oldest first: its timeline. A
// member sees only a request they sent (requestedBy set), as in the list.
func (s *Service) RequestEvents(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) ([]audit.Event, error) {
	if _, err := s.sentRequest(ctx, orgID, id, requestedBy); err != nil {
		return nil, err
	}
	page, err := s.events.ListForTarget(ctx, orgID, audit.TargetIdentityProofingRequest, id.String(), nil, audit.MaxListLimit)
	if err != nil {
		return nil, err
	}
	slices.Reverse(page.Events)
	return page.Events, nil
}

// CreateAPIKey adds a key to a customer; the returned secret is shown once.
func (s *Service) CreateAPIKey(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxAPIKeyNameLength {
		return APIKey{}, "", fmt.Errorf("%w: an API key needs a name of at most %d characters", ErrInvalidInput, maxAPIKeyNameLength)
	}
	return s.apiKeys.Create(ctx, orgID, customerID, createdBy, name)
}

// APIKeys lists a customer's keys, revoked ones included.
func (s *Service) APIKeys(ctx context.Context, orgID, customerID uuid.UUID) ([]APIKey, error) {
	if _, err := s.customers.Get(ctx, orgID, customerID); err != nil {
		return nil, err
	}
	return s.apiKeys.List(ctx, orgID, customerID)
}

// RevokeAPIKey stops one of a customer's keys authenticating.
func (s *Service) RevokeAPIKey(ctx context.Context, orgID, customerID, id uuid.UUID) (APIKey, error) {
	return s.apiKeys.Revoke(ctx, orgID, customerID, id)
}

// AuthenticateAPIKey resolves a raw key to who it acts for.
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string) (APIKeyCaller, error) {
	return s.apiKeys.Authenticate(ctx, raw)
}

// Webhook returns a customer's endpoint, or ErrWebhookNotFound.
func (s *Service) Webhook(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, error) {
	if _, err := s.customers.Get(ctx, orgID, customerID); err != nil {
		return Webhook{}, err
	}
	return s.webhooks.Get(ctx, orgID, customerID)
}

// SaveWebhook sets a customer's endpoint: an absolute https URL (checked again
// at every send, against the address it resolves to) and at least one known
// event. A new endpoint's signing secret is returned, the one time it is
// readable.
func (s *Service) SaveWebhook(ctx context.Context, orgID, customerID uuid.UUID, rawURL string, events []string) (Webhook, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if len(rawURL) > maxWebhookURLLength {
		return Webhook{}, "", fmt.Errorf("%w: the URL is too long", ErrInvalidInput)
	}
	if _, err := (safehttp.Policy{}).CheckURL(rawURL); err != nil {
		return Webhook{}, "", fmt.Errorf("%w: enter an absolute https URL", ErrInvalidInput)
	}
	for _, e := range events {
		if !slices.Contains(WebhookEvents, e) {
			return Webhook{}, "", fmt.Errorf("%w: %q is not a webhook event", ErrInvalidInput, e)
		}
	}
	// Stored in display order, each once.
	picked := slices.DeleteFunc(slices.Clone(WebhookEvents), func(e string) bool { return !slices.Contains(events, e) })
	if len(picked) == 0 {
		return Webhook{}, "", fmt.Errorf("%w: pick at least one event", ErrInvalidInput)
	}
	return s.webhooks.Save(ctx, orgID, customerID, rawURL, picked)
}

// RotateWebhookSecret replaces a customer's signing secret and returns it once.
func (s *Service) RotateWebhookSecret(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, string, error) {
	return s.webhooks.RotateSecret(ctx, orgID, customerID)
}

// RemoveWebhook deletes a customer's endpoint and its waiting deliveries.
func (s *Service) RemoveWebhook(ctx context.Context, orgID, customerID uuid.UUID) error {
	return s.webhooks.Remove(ctx, orgID, customerID)
}

// SendTestWebhook queues a test event for a customer's endpoint.
func (s *Service) SendTestWebhook(ctx context.Context, orgID, customerID uuid.UUID) error {
	return s.webhooks.SendTest(ctx, orgID, customerID)
}

// WebhookDeliveries lists a customer's most recent deliveries.
func (s *Service) WebhookDeliveries(ctx context.Context, orgID, customerID uuid.UUID) ([]Delivery, error) {
	if _, err := s.customers.Get(ctx, orgID, customerID); err != nil {
		return nil, err
	}
	return s.webhooks.Deliveries(ctx, orgID, customerID)
}

// WebhookHealth reports how each of the org's customers' endpoints answers.
func (s *Service) WebhookHealth(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]WebhookHealth, error) {
	return s.webhooks.Health(ctx, orgID)
}

// RemoveCustomer deletes a customer and every request sent for it.
func (s *Service) RemoveCustomer(ctx context.Context, orgID, id uuid.UUID) error {
	return s.customers.Remove(ctx, orgID, id)
}

// SaveCustomerBranding replaces a customer's branding. The colour is a
// #rrggbb hex, the privacy statement an https URL; empty fields fall back.
func (s *Service) SaveCustomerBranding(ctx context.Context, orgID, id uuid.UUID, b CustomerBranding, logo LogoChange) (Customer, error) {
	b.DisplayName = strings.TrimSpace(b.DisplayName)
	b.PrimaryColor = strings.TrimSpace(b.PrimaryColor)
	b.SupportContact = strings.TrimSpace(b.SupportContact)
	b.PrivacyURL = strings.TrimSpace(b.PrivacyURL)
	switch {
	case len(b.DisplayName) > maxCustomerNameLength:
		return Customer{}, fmt.Errorf("%w: the display name is too long", ErrInvalidInput)
	case b.PrimaryColor != "" && !hexColor.MatchString(b.PrimaryColor):
		return Customer{}, fmt.Errorf("%w: the primary colour must be a hex colour like #1f5b4a", ErrInvalidInput)
	case len(b.SupportContact) > maxSupportContactLen:
		return Customer{}, fmt.Errorf("%w: the support contact is too long", ErrInvalidInput)
	case len(b.PrivacyURL) > maxWebhookURLLength:
		return Customer{}, fmt.Errorf("%w: the privacy statement URL is too long", ErrInvalidInput)
	}
	if b.PrivacyURL != "" {
		if _, err := (safehttp.Policy{}).CheckURL(b.PrivacyURL); err != nil {
			return Customer{}, fmt.Errorf("%w: the privacy statement must be an absolute https URL", ErrInvalidInput)
		}
	}
	return s.customers.SaveBranding(ctx, orgID, id, b, logo)
}

// CustomerLogo returns a customer's logo, or ErrNoCustomerLogo.
func (s *Service) CustomerLogo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error) {
	return s.customers.Logo(ctx, orgID, id)
}

// SaveCustomerSettings replaces a customer's session settings; each must be one
// of its options.
func (s *Service) SaveCustomerSettings(ctx context.Context, orgID, id uuid.UUID, settings CustomerSettings) (Customer, error) {
	if !slices.Contains(SessionTTLOptions, settings.SessionTTL) {
		return Customer{}, fmt.Errorf("%w: the session lifetime must be 2, 5 or 10 minutes", ErrInvalidInput)
	}
	if !slices.Contains(DataRetentionDayOptions, settings.DataRetentionDays) {
		return Customer{}, fmt.Errorf("%w: the data retention must be 7, 30 or 90 days", ErrInvalidInput)
	}
	return s.customers.SaveSettings(ctx, orgID, id, settings)
}

// Stats counts the customer requests sent within StatsWindow, per customer and
// flow, narrowed to one member's when requestedBy is set.
func (s *Service) Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]StatsRow, time.Time, error) {
	since := s.now().Add(-StatsWindow)
	rows, err := s.requests.Stats(ctx, orgID, requestedBy, since)
	return rows, since, err
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
