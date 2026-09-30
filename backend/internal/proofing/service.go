package proofing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// Provider is the IPS surface the service drives (implemented by
// *proofingprovider.Client and *proofingprovider.Stub).
type Provider interface {
	CreateTenant(ctx context.Context, id, name string) (proofingprovider.Tenant, error)
	RotateWebhookSecret(ctx context.Context, tenantID string) (string, error)
	CreateAPIKey(ctx context.Context, tenantID string, env proofingprovider.KeyEnvironment, scopes []string) (string, error)
	ListFlows(ctx context.Context, apiKey string) ([]proofingprovider.Flow, error)
	CreateFlow(ctx context.Context, apiKey string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	CreateFlowVersion(ctx context.Context, apiKey, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	ListFlowVersions(ctx context.Context, apiKey, id string) ([]proofingprovider.Flow, error)
	ActivateFlowVersion(ctx context.Context, apiKey, id string, version int) (proofingprovider.Flow, error)
	CreateSession(ctx context.Context, apiKey string, in proofingprovider.SessionInput) (proofingprovider.Session, error)
	SessionStatus(ctx context.Context, apiKey, sessionID, sessionToken string) (proofingprovider.Result, error)
	SessionResult(ctx context.Context, apiKey, sessionID, sessionToken string) (proofingprovider.Result, error)
	SubmitReference(ctx context.Context, apiKey, sessionID, sessionToken string, ref proofingprovider.Reference) (proofingprovider.YiviDisclosure, error)
	SubmitFaceFrame(ctx context.Context, sessionToken, image string) (proofingprovider.FaceVerdict, error)
	DecideReview(ctx context.Context, apiKey, sessionID, sessionToken string, d proofingprovider.ReviewDecision) error
	SessionHandover(ctx context.Context, apiKey, sessionID, sessionToken string) (proofingprovider.Claim, error)
	SessionIdentity(ctx context.Context, apiKey, sessionID, sessionToken string) (proofingprovider.Identity, error)
	CancelSession(ctx context.Context, apiKey, sessionID, sessionToken string) error
	DeleteSession(ctx context.Context, apiKey, sessionID, sessionToken string) error
}

// verifier is the OpenID4VP verifier a Yivi request's disclosure runs at
// (implemented by *openid4vpverifier.Client), the one auth uses.
type verifier interface {
	StartPresentation(ctx context.Context, scope openid4vpverifier.Scope, claims ...string) (openid4vpverifier.Session, error)
	Result(ctx context.Context, transactionID string) (openid4vpverifier.Presentation, error)
}

type settingsStore interface {
	CanStoreSecrets() bool
	APIKey(ctx context.Context, orgID uuid.UUID, mode Mode) (string, error)
	WebhookSecret(ctx context.Context, orgID uuid.UUID) (string, error)
	Provision(ctx context.Context, orgID uuid.UUID, create func(context.Context) (ProvisionedTenant, error)) error
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
	ListPage(ctx context.Context, orgID, customerID uuid.UUID, after *RequestCursor, limit int) ([]Request, error)
	Cancel(ctx context.Context, req Request) (bool, error)
	RecordResultRead(ctx context.Context, req Request) error
	Purge(ctx context.Context, req Request) error
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
	SetYiviTransaction(ctx context.Context, req Request, sessionID, transactionID string) error
	Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID, since time.Time) ([]StatsRow, error)
	GetForCustomer(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]Request, error)
	NextDeadline(ctx context.Context, now time.Time) (time.Time, error)
	GetBySession(ctx context.Context, sessionID string) (Request, error)
	GetByLinkToken(ctx context.Context, hash []byte) (Request, error)
	LapseLinks(ctx context.Context, now time.Time, limit int) (int, error)
	RecordReviewDecision(ctx context.Context, req Request, decided Status, reason, errorCode string) error
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
	DefaultSecret() (string, error)
}

type apiKeyStore interface {
	Create(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string, mode Mode, scopes []string) (APIKey, string, error)
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
	SaveRedirectOrigins(ctx context.Context, orgID, id uuid.UUID, origins []string) (Customer, error)
	Logo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error)
	Remove(ctx context.Context, orgID, id uuid.UUID) error
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service).
type pauseStore interface {
	Get(ctx context.Context, orgID uuid.UUID) (OrgPause, error)
	List(ctx context.Context) ([]OrgPause, error)
	Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, paused bool) (OrgPause, error)
}

type flowHostedStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowHosted, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowHosted) (FlowHosted, error)
}

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
	pauses    pauseStore
	// flowHostedSettings is each flow's hosted page settings; nil the defaults.
	flowHostedSettings flowHostedStore
	ips                Provider
	verifier           verifier
	mailer             Mailer
	now                func() time.Time
	// readChecks throttles the IPS re-check of a single-request read.
	readChecks *readThrottle
	// callbackURL is where IPS pushes session changes (HandleIPSEvent); empty
	// leaves a session to its deadline and to reads.
	callbackURL string
	// hostedBaseURL is the public page a hosted link's token is appended to.
	hostedBaseURL string
}

// SetCallbackURL sets where IPS pushes session changes; call before serving.
func (s *Service) SetCallbackURL(u string) { s.callbackURL = u }

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
// Stores are the service's persistence, one store per concern.
type Stores struct {
	Settings  settingsStore
	Requests  requestStore
	Customers customerStore
	APIKeys   apiKeyStore
	Webhooks  webhookStore
	Events    eventReader
	// Pauses holds who paused an org's proofing; nil never pauses.
	Pauses pauseStore
	// FlowHosted holds each flow's hosted page settings; nil is the defaults.
	FlowHosted flowHostedStore
}

func NewService(stores Stores, ips Provider, verifier verifier, mailer Mailer) *Service {
	return &Service{
		settings: stores.Settings, requests: stores.Requests, customers: stores.Customers, apiKeys: stores.APIKeys,
		webhooks: stores.Webhooks, events: stores.Events, pauses: stores.Pauses, flowHostedSettings: stores.FlowHosted, ips: ips, verifier: verifier, mailer: mailer, now: time.Now,
		readChecks: newReadThrottle(readReconcileEvery),
	}
}

// orgAPIKey returns the org's live IPS API key, provisioning the org's IPS
// tenant on its first use: an org needs no enable step before it can proof.
func (s *Service) orgAPIKey(ctx context.Context, org Org) (string, error) {
	return s.orgKey(ctx, org, ModeLive)
}

// orgKey is orgAPIKey for mode: the test key runs the org's test requests,
// scripted at IPS, on the same tenant.
func (s *Service) orgKey(ctx context.Context, org Org, mode Mode) (string, error) {
	apiKey, err := s.settings.APIKey(ctx, org.ID, mode)
	if !errors.Is(err, ErrNotProvisioned) {
		return apiKey, err
	}
	if err := s.provision(ctx, org); err != nil {
		return "", err
	}
	return s.settings.APIKey(ctx, org.ID, mode)
}

// requestAPIKey is the IPS key req's session runs under.
func (s *Service) requestAPIKey(ctx context.Context, req Request) (string, error) {
	return s.settings.APIKey(ctx, req.OrganizationID, req.mode())
}

// provision creates the org's IPS tenant under the org's own id, with a live
// and a test key, and stores them. The encryption key is checked first, so a
// deployment without one never leaves an orphaned tenant at IPS. A tenant IPS
// already has (a first use whose save was lost) is taken over with a fresh
// webhook secret and fresh keys.
func (s *Service) provision(ctx context.Context, org Org) error {
	if !s.settings.CanStoreSecrets() {
		return ErrNoEncryptionKey
	}
	id := org.ID.String()
	return s.settings.Provision(ctx, org.ID, func(ctx context.Context) (ProvisionedTenant, error) {
		var out ProvisionedTenant
		tenant, err := s.ips.CreateTenant(ctx, id, org.Name)
		out.WebhookSecret = tenant.WebhookSecret
		if errors.Is(err, proofingprovider.ErrTenantExists) {
			out.WebhookSecret, err = s.ips.RotateWebhookSecret(ctx, id)
		}
		if err != nil {
			return out, fmt.Errorf("proofing: provision tenant org %s: %w", org.ID, err)
		}
		if out.LiveKey, err = s.ips.CreateAPIKey(ctx, id, proofingprovider.KeyLive, proofingprovider.TenantKeyScopes); err != nil {
			return out, fmt.Errorf("proofing: provision live api key org %s: %w", org.ID, err)
		}
		if out.TestKey, err = s.ips.CreateAPIKey(ctx, id, proofingprovider.KeyTest, proofingprovider.TenantKeyScopes); err != nil {
			return out, fmt.Errorf("proofing: provision test api key org %s: %w", org.ID, err)
		}
		return out, nil
	})
}

// Flows returns the org's active IPS flows with the admin's selection applied.
// all lists every flow (the admin's view); otherwise only the flows members may
// send a request on.
func (s *Service) Flows(ctx context.Context, org Org, all bool) ([]OrgFlow, error) {
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return nil, err
	}
	return s.orgFlows(ctx, org, apiKey, all)
}

// orgFlows is Flows with the org's IPS key already resolved.
func (s *Service) orgFlows(ctx context.Context, org Org, apiKey string, all bool) ([]OrgFlow, error) {
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
	if !hasFaceStep(in.Steps) {
		in.FaceProvider = ""
		return in, checkReachable(in)
	}
	in.SelfieLocation = selfieLocationNative
	if in.FaceProvider == "" {
		in.FaceProvider = faceProviderRegula
	}
	if !slices.Contains(faceProviders, in.FaceProvider) {
		return in, fmt.Errorf("%w: unknown face provider %q", ErrInvalidInput, in.FaceProvider)
	}
	return in, checkReachable(in)
}

// checkReachable refuses a flow whose required level its steps can never
// reach, which would fail every request with ErrorAssuranceNotMet. IPS
// (computeEIDASAssuranceLevel) reports substantial only for a chip read plus a
// Regula face check against the chip's portrait; Validate at IPS accepts
// weaker flows, as it checks the declared checks and not the face provider.
func checkReachable(in proofingprovider.FlowSpec) error {
	if in.RequiredAssuranceLevel != eidasSubstantial {
		return nil
	}
	if !slices.Contains(in.Steps, stepNFCRead) || !hasFaceStep(in.Steps) || in.FaceProvider != faceProviderRegula {
		return fmt.Errorf("%w: substantial needs the chip read and a face step verified by Regula", ErrInvalidInput)
	}
	return nil
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
	// HostedURL is a hosted request's link to its public page.
	HostedURL string
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
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return Sent{}, err
	}
	flow, err := s.sendableFlow(ctx, org, apiKey, in.FlowID, customer)
	if err != nil {
		return Sent{}, err
	}
	if in.Method == proofingprovider.MethodYivi && !YiviAppAvailable(flow) {
		return Sent{}, fmt.Errorf("%w: this flow's face provider only runs in the Idem app", ErrInvalidInput)
	}
	if in.Channel == ChannelHosted {
		return s.createHosted(ctx, org, by, in, subject, *customer, flow)
	}

	id := uuid.New()
	sessionKey, input := apiKey, proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: id.String(), TTL: ttl, Method: in.Method, CallbackURL: s.callbackURL,
		Language: string(in.Language),
	}
	if in.Mode == ModeTest {
		// A test key's session runs no flow: its outcome is scripted at IPS.
		if sessionKey, err = s.orgKey(ctx, org, ModeTest); err != nil {
			return Sent{}, err
		}
		input.FlowID, input.ScriptedOutcome = "", in.ScriptedOutcome
	}
	sess, err := s.ips.CreateSession(ctx, sessionKey, input)
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: %w", id, err)
	}
	// An Idem session is the vcmrtd link; a Yivi one is started from the screen.
	// A scripted one is resolved already and has neither.
	if in.Mode == ModeLive && in.Method == proofingprovider.MethodIdem && sess.Claim == nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: IPS offered no vcmrtd link", id)
	}
	req, err := s.requests.Create(ctx, NewStoredRequest{
		ID: id, OrgID: org.ID, RequestedBy: by.userID(), APIKeyID: by.APIKeyID, Subject: subject,
		Flow: flow, LinkExpiresAt: sess.ExpiresAt, Method: in.Method, Channel: in.Channel, Mode: in.Mode,
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
	if in.Mode == ModeTest {
		// Record the scripted outcome now rather than on IPS's push; a failure
		// is logged and the push or the deadline job records it instead.
		req, _ = s.tryReconcile(ctx, sessionKey, req)
		return Sent{Request: req}, nil
	}

	out := Sent{Request: req}
	if sess.Claim == nil {
		return out, nil
	}
	out.DeepLink = sess.Claim.DeepLink
	if s.mailer != nil && !in.SkipMail && in.Channel == ChannelEmail {
		err := s.mailer.SendIdentityProofingRequested(ctx, org.ID, s.proofingMail(ctx, org, customer, subject, by, sess.Claim.DeepLink, ttl, in.Language))
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: request e-mail not sent",
				slog.String("org_id", org.ID.String()), slog.String("request_id", req.ID.String()), slog.Any("error", err))
		}
		out.MailSent = err == nil
	}
	return out, nil
}

// createHosted stores a hosted request with its link and no IPS session yet:
// the subject starts one from the page (StartHosted) until HostedLinkTTL. Its
// redirect must be on one of the customer's allowed origins.
func (s *Service) createHosted(ctx context.Context, org Org, by Requester, in NewRequest, subject Subject,
	customer Customer, flow proofingprovider.Flow,
) (Sent, error) {
	if s.hostedBaseURL == "" {
		return Sent{}, errors.New("proofing: no hosted page URL configured")
	}
	hosted, err := s.flowHosted(ctx, org.ID, flow.ID)
	if err != nil {
		return Sent{}, err
	}
	if !hosted.Enabled {
		return Sent{}, ErrHostedDisabled
	}
	if in.Language != "" && !hosted.Offers(in.Language) {
		return Sent{}, fmt.Errorf("%w: this flow's hosted page is not offered in %q", ErrInvalidInput, in.Language)
	}
	if in.RedirectURL != "" {
		if hosted.Completion == CompletionDone {
			return Sent{}, fmt.Errorf("%w: this flow's hosted page ends on its own thank-you page, without a redirect", ErrInvalidInput)
		}
		if err := checkRedirect(in.RedirectURL, customer); err != nil {
			return Sent{}, err
		}
	}
	token, hash, err := newLinkToken()
	if err != nil {
		return Sent{}, err
	}
	req, err := s.requests.Create(ctx, NewStoredRequest{
		ID: uuid.New(), OrgID: org.ID, RequestedBy: by.userID(), APIKeyID: by.APIKeyID, Subject: subject,
		Flow: flow, LinkExpiresAt: s.now().Add(HostedLinkTTL), Method: in.Method, Channel: in.Channel,
		Mode: in.Mode, LinkTokenHash: hash, RedirectURL: in.RedirectURL, Language: in.Language,
	})
	if err != nil {
		return Sent{}, err
	}
	return Sent{Request: req, HostedURL: s.hostedBaseURL + token}, nil
}

// normalizeRequest fills in the default app and delivery and refuses a
// combination that cannot run: the Yivi app's face check runs in the browser
// that shows its QR, and a mailed subject has no such page.
func normalizeRequest(in NewRequest) (NewRequest, error) {
	if in.Method == "" {
		in.Method = proofingprovider.MethodIdem
	}
	if in.Mode == "" {
		in.Mode = ModeLive
	}
	switch {
	case in.Mode == ModeTest && in.ScriptedOutcome == "":
		in.ScriptedOutcome = defaultScriptedOutcome
	case in.Mode == ModeTest && !scriptedOutcomePattern.MatchString(in.ScriptedOutcome):
		return NewRequest{}, fmt.Errorf("%w: scriptedOutcome is approve, reject:<CODE>, needs_review or expire", ErrInvalidInput)
	case in.Mode != ModeTest && in.ScriptedOutcome != "":
		return NewRequest{}, fmt.Errorf("%w: only a test key can script an outcome", ErrInvalidInput)
	}
	if in.Channel == "" {
		in.Channel = ChannelEmail
	}
	if in.Language != "" {
		locale, ok := email.ParseLocale(string(in.Language))
		if !ok {
			return NewRequest{}, fmt.Errorf("%w: unsupported language %q", ErrInvalidInput, in.Language)
		}
		in.Language = locale
	}
	switch {
	case in.Method != proofingprovider.MethodIdem && in.Method != proofingprovider.MethodYivi:
		return NewRequest{}, fmt.Errorf("%w: choose the Idem app or the Yivi app", ErrInvalidInput)
	case in.Channel != ChannelEmail && in.Channel != ChannelOnScreen && in.Channel != ChannelHosted:
		return NewRequest{}, fmt.Errorf("%w: send the request by e-mail, show it on screen or as a link", ErrInvalidInput)
	case in.Method == proofingprovider.MethodYivi && in.Channel == ChannelEmail:
		return NewRequest{}, fmt.Errorf("%w: a Yivi app session runs on this screen and cannot be e-mailed", ErrInvalidInput)
	case in.Channel == ChannelHosted && in.CustomerID == nil:
		return NewRequest{}, fmt.Errorf("%w: a link goes to a customer's subject", ErrInvalidInput)
	case in.Channel == ChannelHosted && in.Mode == ModeTest:
		return NewRequest{}, fmt.Errorf("%w: a test session resolves at once and has no link", ErrInvalidInput)
	case in.RedirectURL != "" && in.Channel != ChannelHosted:
		return NewRequest{}, fmt.Errorf("%w: only a hosted session redirects its subject", ErrInvalidInput)
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
	if in.Mode == ModeLive && !customer.HasLiveKey {
		return Subject{}, nil, ErrCustomerNoAPIKey
	}
	name := strings.TrimSpace(in.SubjectName)
	if len(name) > maxSubjectNameLength {
		return Subject{}, nil, fmt.Errorf("%w: the name is too long", ErrInvalidInput)
	}
	if in.Channel != ChannelEmail && strings.TrimSpace(in.SubjectEmail) == "" {
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
	deepLink string, ttl time.Duration, locale email.Locale,
) email.ProofingMail {
	m := email.ProofingMail{
		To: subject.Email, OrgName: org.Name, RequesterName: by.Name, DeepLink: deepLink, ValidFor: ttl, Locale: locale,
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
// member (customer nil), a flow the admin made available to members; for a
// customer's subject, one assigned to that customer, its default when flowID is
// empty. Either way it must be one a recipient can finish.
func (s *Service) sendableFlow(ctx context.Context, org Org, apiKey, flowID string, customer *Customer) (proofingprovider.Flow, error) {
	if customer == nil {
		flows, err := s.orgFlows(ctx, org, apiKey, true)
		if err != nil {
			return proofingprovider.Flow{}, err
		}
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == flowID })
		if i < 0 {
			return proofingprovider.Flow{}, ErrFlowNotFound
		}
		if err := sendable(flows[i].Flow, flows[i].Allowed, ErrFlowNotAllowed); err != nil {
			return proofingprovider.Flow{}, err
		}
		return flows[i].Flow, nil
	}
	if flowID == "" {
		// An API caller that names no flow gets the customer's default.
		if flowID = customer.Flows.DefaultFlowID; flowID == "" {
			return proofingprovider.Flow{}, ErrFlowNotAssigned
		}
	}
	flows, err := s.ips.ListFlows(ctx, apiKey)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	i := slices.IndexFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == flowID })
	if i < 0 {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	if err := sendable(flows[i], slices.Contains(customer.Flows.FlowIDs, flowID), ErrFlowNotAssigned); err != nil {
		return proofingprovider.Flow{}, err
	}
	return flows[i], nil
}

// sendable refuses a flow no recipient can finish, then one the sender may not
// use (notPermitted).
func sendable(flow proofingprovider.Flow, permitted bool, notPermitted error) error {
	if !Completable(flow) {
		return ErrFlowNotCompletable
	}
	if !permitted {
		return notPermitted
	}
	return nil
}

// Requests lists the org's requests narrowed by filter, as stored: outcomes
// land by IPS's push and the deadline job, so a list read never calls IPS.
func (s *Service) Requests(ctx context.Context, orgID uuid.UUID, filter RequestFilter) ([]Request, error) {
	return s.requests.List(ctx, orgID, filter)
}

// reconcile reads the request's IPS session and records what IPS decided,
// with no actor: the audit trail shows it as the system's. An IPS
// failure is logged and the row is shown as last known: a read must not fail
// because IPS is briefly away.
func (s *Service) reconcile(ctx context.Context, apiKey string, req Request) Request {
	req, _ = s.tryReconcile(ctx, apiKey, req)
	return req
}

// tryReconcile is reconcile that also returns the (already logged) failure.
func (s *Service) tryReconcile(ctx context.Context, apiKey string, req Request) (Request, error) {
	// What IPS reports is the subject's doing and the wallet's record of it, not
	// that of whoever's read happened to trigger the check.
	ctx = audit.WithoutActor(ctx)
	sess := req.session
	res, err := s.ips.SessionStatus(ctx, apiKey, sess.ID, sess.Token)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		// IPS purged or erased the session: treat it like a lapsed one.
		res, err = proofingprovider.Result{Status: proofingprovider.StatusExpired}, nil
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: reconcile failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req, err
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
			return req, nil
		}
		if err := s.requests.MarkStarted(ctx, req, sess.ID, res.Method); err != nil {
			slog.WarnContext(ctx, "identity proofing: mark started failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		req.Status = StatusInProgress
		req.Method = methodOr(res.Method, req.Method)
		return req, nil
	case proofingprovider.StatusExpired, proofingprovider.StatusCancelled:
		// needs_review is still open at IPS, so it can end there too.
		if req.Status == StatusApproved || req.Status == StatusRejected {
			return req, nil
		}
		// The session ended undecided, and with it the request: a new one means
		// a new mail.
		if err := s.requests.EndSession(ctx, req, sess.ID, res.Status, res.Method); err != nil {
			slog.WarnContext(ctx, "identity proofing: end session failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		ended, now := *sess, s.now()
		ended.EndedAt = &now
		req.session = &ended
		req.Method = methodOr(res.Method, req.Method)
		return req, nil
	default:
		return req, nil
	}
	if next == StatusApproved {
		next, res = enforceAssurance(req, res)
	}
	if next == req.Status {
		return req, nil
	}
	// The name read off the document is kept only for a customer's subject the
	// sender may know only by address, and only once the document is approved:
	// only then is the full result, with its personal data, read at all.
	if req.CustomerID != nil && next == StatusApproved {
		full, err := s.ips.SessionResult(ctx, apiKey, sess.ID, sess.Token)
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: read the approved result failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		res.Name = full.Name
	}
	if err := s.requests.RecordOutcome(ctx, req, sess.ID, next, res); err != nil {
		slog.WarnContext(ctx, "identity proofing: record outcome failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req, err
	}
	req.Status, req.AssuranceLevel, req.EIDASLevel, req.ErrorCode = next, res.AssuranceLevel, res.EIDASLevel, res.ErrorCode
	req.ProofedName = res.Name
	req.Method = methodOr(res.Method, req.Method)
	completedAt := s.now()
	if res.CompletedAt != nil {
		completedAt = *res.CompletedAt
	}
	req.CompletedAt = &completedAt
	return req, nil
}

// enforceAssurance holds an approval IPS reported to the level the request's
// flow demanded: IPS approves on its checks alone and never compares the level
// achieved with the required one. An approval that falls short is a rejection
// with ErrorAssuranceNotMet. A Yivi session an older IPS did not score counts
// as yiviEIDASLevel.
func enforceAssurance(req Request, res proofingprovider.Result) (Status, proofingprovider.Result) {
	if res.EIDASLevel == "" && methodOr(res.Method, req.Method) == proofingprovider.MethodYivi {
		res.EIDASLevel = yiviEIDASLevel
	}
	if MeetsAssurance(res.EIDASLevel, req.RequiredAssuranceLevel) {
		return StatusApproved, res
	}
	res.ErrorCode = ErrorAssuranceNotMet
	return StatusRejected, res
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

// CustomerRequest is one of a customer's requests; see readReconciled.
func (s *Service) CustomerRequest(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error) {
	req, err := s.requests.GetForCustomer(ctx, orgID, customerID, id)
	if err != nil {
		return Request{}, err
	}
	return s.readReconciled(ctx, req)
}

// StoredCustomerRequest is one of a customer's requests as stored, without
// re-checking IPS: what a headless poll answers from.
func (s *Service) StoredCustomerRequest(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error) {
	return s.requests.GetForCustomer(ctx, orgID, customerID, id)
}

// CustomerRequestPage is a page of a customer's requests, newest first, from
// stored state (no IPS call), and the cursor of the next page ("" at the end).
// A cursor that does not decode is ErrInvalidInput.
func (s *Service) CustomerRequestPage(ctx context.Context, orgID, customerID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	limit = min(limit, MaxPageSize)
	var after *RequestCursor
	if cursor != "" {
		c, ok := decodeRequestCursor(cursor)
		if !ok {
			return nil, "", fmt.Errorf("%w: the cursor is not one this API gave", ErrInvalidInput)
		}
		after = &c
	}
	// One extra row tells whether a next page exists.
	reqs, err := s.requests.ListPage(ctx, orgID, customerID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(reqs) <= limit {
		return reqs, "", nil
	}
	reqs = reqs[:limit]
	last := reqs[limit-1]
	return reqs, encodeRequestCursor(RequestCursor{CreatedAt: last.CreatedAt, ID: last.ID}), nil
}

// CancelRequest ends a customer's request that has no outcome yet, at IPS too,
// and marks it cancelled. One that has an outcome or ended is ErrSessionOver.
func (s *Service) CancelRequest(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, error) {
	req, err := s.CustomerRequest(ctx, orgID, customerID, id)
	if err != nil {
		return Request{}, err
	}
	if st := req.EffectiveStatus(s.now()); st != StatusPending && st != StatusInProgress || req.PurgedAt != nil {
		return Request{}, ErrSessionOver
	}
	if sess := req.liveSession(s.now()); sess != nil {
		apiKey, err := s.requestAPIKey(ctx, req)
		if err != nil {
			return Request{}, err
		}
		if err := s.ips.CancelSession(ctx, apiKey, sess.ID, sess.Token); err != nil {
			var rejected *proofingprovider.RejectedError
			if errors.As(err, &rejected) {
				// IPS decided it first; the pushed outcome records that.
				return Request{}, ErrSessionOver
			}
			return Request{}, fmt.Errorf("proofing: cancel request %s: %w", req.ID, err)
		}
	}
	cancelled, err := s.requests.Cancel(ctx, req)
	if err != nil {
		return Request{}, err
	}
	if !cancelled {
		return Request{}, ErrSessionOver
	}
	return s.requests.GetForCustomer(ctx, orgID, customerID, id)
}

// RequestResult reads a settled customer request's result from IPS, the
// wallet storing no identity, and audits identity_proofing.result_read. One
// not settled yet is ErrResultNotReady; one erased or gone at IPS is not found.
func (s *Service) RequestResult(ctx context.Context, orgID, customerID, id uuid.UUID) (Request, proofingprovider.Identity, error) {
	req, err := s.CustomerRequest(ctx, orgID, customerID, id)
	if err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	return s.requestResult(ctx, req)
}

// AdminRequestResult is RequestResult for an org admin reading one of the org's
// customer requests in the wallet: audited identity_proofing.result_read with
// the admin as actor. A member's request is not found: its subject is a
// colleague, whose identity the wallet shows nowhere.
func (s *Service) AdminRequestResult(ctx context.Context, orgID, id uuid.UUID) (Request, proofingprovider.Identity, error) {
	req, err := s.Request(ctx, orgID, id, nil)
	if err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	if req.CustomerID == nil {
		return Request{}, proofingprovider.Identity{}, ErrRequestNotFound
	}
	return s.requestResult(ctx, req)
}

func (s *Service) requestResult(ctx context.Context, req Request) (Request, proofingprovider.Identity, error) {
	if req.PurgedAt != nil || req.session == nil {
		return Request{}, proofingprovider.Identity{}, ErrRequestNotFound
	}
	if !req.Status.Settled() {
		return Request{}, proofingprovider.Identity{}, ErrResultNotReady
	}
	apiKey, err := s.requestAPIKey(ctx, req)
	if err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	identity, err := s.ips.SessionIdentity(ctx, apiKey, req.session.ID, req.session.Token)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		return Request{}, proofingprovider.Identity{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, proofingprovider.Identity{}, fmt.Errorf("proofing: result request %s: %w", req.ID, err)
	}
	if err := s.requests.RecordResultRead(ctx, req); err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	return req, identity, nil
}

// PurgeRequest erases a customer's request at IPS and what the wallet holds
// of its outcome, whatever its state; the row stays, marked purged.
func (s *Service) PurgeRequest(ctx context.Context, orgID, customerID, id uuid.UUID) error {
	req, err := s.requests.GetForCustomer(ctx, orgID, customerID, id)
	if err != nil {
		return err
	}
	if req.PurgedAt != nil {
		return nil
	}
	if req.session != nil {
		apiKey, err := s.requestAPIKey(ctx, req)
		if err != nil {
			return err
		}
		if err := s.ips.DeleteSession(ctx, apiKey, req.session.ID, req.session.Token); err != nil {
			return fmt.Errorf("proofing: purge request %s: %w", req.ID, err)
		}
	}
	return s.requests.Purge(ctx, req)
}

// Request is one of the org's requests, what an on-screen session's page
// polls; see readReconciled. A member sees only a request they sent
// (requestedBy set), as in the list.
func (s *Service) Request(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (Request, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return Request{}, err
	}
	return s.readReconciled(ctx, req)
}

// readReconciled re-checks a request that may have moved on at IPS, at most
// once per readReconcileEvery: a fallback for a missed push, so a poller
// never turns into one IPS read per poll.
func (s *Service) readReconciled(ctx context.Context, req Request) (Request, error) {
	if !req.needsReconcile() || !s.readChecks.allow(req.ID, s.now()) {
		return req, nil
	}
	apiKey, err := s.requestAPIKey(ctx, req)
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
	return s.yiviSessionOf(req)
}

// yiviSessionOf is req's running IPS session, if req is for the Yivi app.
func (s *Service) yiviSessionOf(req Request) (Request, *ipsSession, error) {
	if req.Method != proofingprovider.MethodYivi {
		return Request{}, nil, ErrWrongMethod
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return Request{}, nil, ErrSessionOver
	}
	return req, sess, nil
}

// ClaimLink is a fresh vcmrtd link for a sent Idem request: a new claim once
// the first lapsed unscanned, or a handover to another phone once the app that
// held the session left. An app still active is ErrDeviceActive.
func (s *Service) ClaimLink(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (proofingprovider.Claim, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return proofingprovider.Claim{}, err
	}
	return s.claimLink(ctx, req)
}

func (s *Service) claimLink(ctx context.Context, req Request) (proofingprovider.Claim, error) {
	// A test request resolved at once and never had a link.
	if req.Method != proofingprovider.MethodIdem || req.mode() == ModeTest {
		return proofingprovider.Claim{}, ErrWrongMethod
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return proofingprovider.Claim{}, ErrSessionOver
	}
	apiKey, err := s.requestAPIKey(ctx, req)
	if err != nil {
		return proofingprovider.Claim{}, err
	}
	claim, err := s.ips.SessionHandover(ctx, apiKey, sess.ID, sess.Token)
	var rejected *proofingprovider.RejectedError
	switch {
	case errors.As(err, &rejected) && rejected.Code == proofingprovider.CodeDeviceActive:
		return proofingprovider.Claim{}, ErrDeviceActive
	case errors.As(err, &rejected):
		return proofingprovider.Claim{}, ErrSessionOver
	case err != nil:
		return proofingprovider.Claim{}, fmt.Errorf("proofing: claim link request %s: %w", req.ID, err)
	}
	return claim, nil
}

// YiviStart is the OpenID4VP presentation an on-screen Yivi request asks for.
// WalletLink is the openid4vp:// request the subject's Yivi app opens (the
// page shows it as a QR and a universal link); ExpiresAt the session's cap.
type YiviStart struct {
	WalletLink string
	ExpiresAt  time.Time
}

// StartYivi starts (or, after a cancel in the app, restarts) the OpenID4VP
// disclosure of an on-screen Yivi request's passport or id-card and its photo.
// The verifier's transaction stays on the request, server-side.
func (s *Service) StartYivi(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (YiviStart, error) {
	req, sess, err := s.yiviSession(ctx, orgID, id, requestedBy)
	if err != nil {
		return YiviStart{}, err
	}
	return s.startYivi(ctx, req, sess)
}

func (s *Service) startYivi(ctx context.Context, req Request, sess *ipsSession) (YiviStart, error) {
	started, err := s.verifier.StartPresentation(ctx, openid4vpverifier.ScopeProofing)
	if err != nil {
		return YiviStart{}, err
	}
	if err := s.requests.SetYiviTransaction(ctx, req, sess.ID, started.TransactionID); err != nil {
		return YiviStart{}, err
	}
	return YiviStart{WalletLink: started.WalletLink, ExpiresAt: sess.ExpiresAt}, nil
}

// YiviDisclosure hands the subject's finished disclosure to IPS as the face
// check's reference, or answers ErrDisclosurePending while the subject has not
// finished in the Yivi app. A disclosure IPS cannot use (no photo, no face in
// it) ends the session, which is recorded at once. The photo and claims pass
// through and are not kept.
func (s *Service) YiviDisclosure(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (proofingprovider.YiviDisclosure, error) {
	req, sess, err := s.yiviSession(ctx, orgID, id, requestedBy)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	return s.yiviDisclosure(ctx, req, sess)
}

func (s *Service) yiviDisclosure(ctx context.Context, req Request, sess *ipsSession) (proofingprovider.YiviDisclosure, error) {
	if req.yiviTransactionID == "" {
		return proofingprovider.YiviDisclosure{}, ErrDisclosurePending
	}
	presentation, err := s.verifier.Result(ctx, req.yiviTransactionID)
	if errors.Is(err, openid4vpverifier.ErrPending) {
		return proofingprovider.YiviDisclosure{}, ErrDisclosurePending
	}
	if err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	doc, ok := presentation.Document()
	if !ok {
		return proofingprovider.YiviDisclosure{}, errors.New("proofing: the presentation carries no passport or id-card")
	}
	apiKey, err := s.requestAPIKey(ctx, req)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	attributes := maps.Clone(doc.Claims)
	delete(attributes, openid4vpverifier.ClaimPhoto)
	disclosure, err := s.ips.SubmitReference(ctx, apiKey, sess.ID, sess.Token, proofingprovider.Reference{
		Credential: doc.Credential, Photo: doc.Claims[openid4vpverifier.ClaimPhoto], Attributes: attributes,
	})
	if err != nil {
		return proofingprovider.YiviDisclosure{}, yiviStepError(err)
	}
	s.reconcile(ctx, apiKey, req)
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
	return s.faceFrame(ctx, req, sess, image)
}

func (s *Service) faceFrame(ctx context.Context, req Request, sess *ipsSession, image string) (proofingprovider.FaceVerdict, error) {
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
	apiKey, err := s.requestAPIKey(ctx, req)
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

// CreateAPIKey adds a live or test key (mode empty is live) with every scope to
// a customer; the returned secret is shown once.
func (s *Service) CreateAPIKey(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string, mode Mode) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxAPIKeyNameLength {
		return APIKey{}, "", fmt.Errorf("%w: an API key needs a name of at most %d characters", ErrInvalidInput, maxAPIKeyNameLength)
	}
	if mode == "" {
		mode = ModeLive
	}
	if mode != ModeLive && mode != ModeTest {
		return APIKey{}, "", fmt.Errorf("%w: an API key is live or test", ErrInvalidInput)
	}
	return s.apiKeys.Create(ctx, orgID, customerID, createdBy, name, mode, slices.Clone(APIKeyScopes))
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

// ReceiveDefaultWebhook takes a delivery at the wallet's own endpoint: the
// default for a customer without one. Its result is already stored where the
// event came from, so a delivery signed with the default secret is only
// acknowledged; any other is ErrBadSignature.
func (s *Service) ReceiveDefaultWebhook(body []byte, signature string) error {
	secret, err := s.webhooks.DefaultSecret()
	if err != nil {
		return err
	}
	return verifyWebhookSignature(secret, signature, body, s.now())
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
		return Customer{}, fmt.Errorf("%w: the data retention must be 7, 30, 90, 180 or 365 days", ErrInvalidInput)
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
