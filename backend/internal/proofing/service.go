package proofing

import (
	"context"
	"encoding/base64"
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

// Provider is the proofing engine the service drives (implemented by
// *proofingengine.Engine, and by *proofingprovider.Stub in tests). Every call
// names the org it is for as the engine's tenant.
type Provider interface {
	ListFlows(ctx context.Context, t proofingprovider.Tenant) ([]proofingprovider.Flow, error)
	CreateFlow(ctx context.Context, t proofingprovider.Tenant, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	CreateFlowVersion(ctx context.Context, t proofingprovider.Tenant, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error)
	ListFlowVersions(ctx context.Context, t proofingprovider.Tenant, id string) ([]proofingprovider.Flow, error)
	ActivateFlowVersion(ctx context.Context, t proofingprovider.Tenant, id string, version int) (proofingprovider.Flow, error)
	CreateSession(ctx context.Context, t proofingprovider.Tenant, in proofingprovider.SessionInput) (proofingprovider.Session, error)
	SessionStatus(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Result, error)
	SessionResult(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Result, error)
	SubmitReference(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string, ref proofingprovider.Reference) (proofingprovider.YiviDisclosure, error)
	SubmitFaceFrame(ctx context.Context, sessionToken, image string) (proofingprovider.FaceVerdict, error)
	DecideReview(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string, d proofingprovider.ReviewDecision) error
	SessionHandover(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Claim, error)
	SessionIdentity(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Identity, error)
	CancelSession(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) error
	DeleteSession(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) error
}

// verifier is the OpenID4VP verifier a Yivi request's disclosure runs at
// (implemented by *openid4vpverifier.Client), the one auth uses.
type verifier interface {
	StartPresentation(ctx context.Context, scope openid4vpverifier.Scope, claims ...string) (openid4vpverifier.Session, error)
	Result(ctx context.Context, transactionID string) (openid4vpverifier.Presentation, error)
}

type settingsStore interface {
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
	ListPage(ctx context.Context, scope CustomerScope, after *RequestCursor, limit int) ([]Request, error)
	Cancel(ctx context.Context, req Request) (bool, error)
	RecordResultRead(ctx context.Context, req Request) error
	Purge(ctx context.Context, req Request) error
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
	SetYiviTransaction(ctx context.Context, req Request, sessionID, transactionID string) error
	ReferencePhoto(ctx context.Context, req Request) (*proofingprovider.Image, error)
	Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID, since time.Time) ([]StatsRow, error)
	GetForCustomer(ctx context.Context, scope CustomerScope, id uuid.UUID) (Request, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]Request, error)
	NextDeadline(ctx context.Context, now time.Time) (time.Time, error)
	GetBySession(ctx context.Context, sessionID string) (Request, error)
	GetByLinkToken(ctx context.Context, hash []byte) (Request, error)
	LapseLinks(ctx context.Context, now time.Time, limit int) (int, error)
	RecordReviewDecision(ctx context.Context, req Request, decided Status, reason, errorCode string) error
	RecordHandover(ctx context.Context, req Request, expiresAt time.Time) error
	ListPurgeDue(ctx context.Context, limit int) ([]Request, error)
	ListUnpurgedForCustomer(ctx context.Context, orgID, customerID uuid.UUID) ([]Request, error)
	ClearExpiredProofedNames(ctx context.Context) (int64, error)
	ListOpenReviews(ctx context.Context, orgID uuid.UUID, customerID *uuid.UUID) ([]Request, error)
	LockReview(ctx context.Context, id uuid.UUID) (func(), error)
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
	Create(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string, scopes []string) (APIKey, string, error)
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
	SetStatus(ctx context.Context, orgID, id uuid.UUID, status CustomerStatus) (Customer, error)
	SaveSettings(ctx context.Context, orgID, id uuid.UUID, settings CustomerSettings) (Customer, error)
	SaveBranding(ctx context.Context, orgID, id uuid.UUID, b CustomerBranding, logo LogoChange) (Customer, error)
	SaveRedirectOrigins(ctx context.Context, orgID, id uuid.UUID, origins []string) (Customer, error)
	Logo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error)
	Remove(ctx context.Context, orgID, id uuid.UUID) error
}

type pauseStore interface {
	Get(ctx context.Context, orgID uuid.UUID) (OrgPause, error)
	List(ctx context.Context) ([]OrgPause, error)
	Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, state PauseState, by *uuid.UUID) (OrgPause, error)
}

type flowHostedStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowHosted, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowHosted) (FlowHosted, error)
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service).
type Mailer interface {
	SendIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, m email.ProofingMail) error
}

// Service orchestrates the engine, the settings, request and customer stores, and the mailer.
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
	// flowDiplomaSettings is each flow's DiplomaMode, diplomas the extracts
	// requests hold; nil asks for none.
	flowDiplomaSettings flowDiplomaStore
	diplomas            diplomaStore
	diplomaChecker      diplomaChecker
	ips                 Provider
	verifier            verifier
	mailer              Mailer
	now                 func() time.Time
	// readChecks throttles the engine re-check of a single-request read.
	readChecks *readThrottle
	// hostedBaseURL is the public page a hosted link's token is appended to.
	hostedBaseURL string
	// dataRequests holds each flow's kind and a data request's matches; nil
	// makes every flow an identity check.
	dataRequests dataRequestStore
}

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
	// FlowDiplomas holds each flow's DiplomaMode and Diplomas the extracts
	// requests hold; nil asks for none.
	FlowDiplomas flowDiplomaStore
	Diplomas     diplomaStore
	// DataRequests holds each flow's kind and a data request's matches; nil
	// makes every flow an identity check.
	DataRequests dataRequestStore
}

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
func NewService(stores Stores, ips Provider, verifier verifier, mailer Mailer) *Service {
	return &Service{
		settings: stores.Settings, requests: stores.Requests, customers: stores.Customers, apiKeys: stores.APIKeys,
		webhooks: stores.Webhooks, events: stores.Events, pauses: stores.Pauses, flowHostedSettings: stores.FlowHosted,
		flowDiplomaSettings: stores.FlowDiplomas, diplomas: stores.Diplomas, dataRequests: stores.DataRequests,
		ips: ips, verifier: verifier, mailer: mailer, now: time.Now,
		readChecks: newReadThrottle(readReconcileEvery),
	}
}

// orgTenant is the org as the engine's tenant: its own id. An org needs no
// enable step before it can proof.
func orgTenant(orgID uuid.UUID) proofingprovider.Tenant {
	return proofingprovider.Tenant{ID: orgID.String()}
}

// retentionGrace is how much longer the engine keeps a customer's session
// than the customer's data retention, so PurgeDue (which erases it in the
// engine too) always runs first.
const retentionGrace = 24 * time.Hour

// engineRetention is how long the engine keeps a session once it ended: the
// flow's own override when it set one, else the customer's data retention,
// plus grace; a member's request leaves it to the engine (the flow's
// override, or its default).
func engineRetention(customer *Customer, override time.Duration) time.Duration {
	if customer == nil {
		return 0
	}
	retention := customer.Settings.DataRetention()
	if override > 0 {
		retention = override
	}
	return retention + retentionGrace
}

// subjectRetentionDays is what the subject is told about their session's data
// on a customer's flow: how long it is kept anywhere at most, the retention
// that applies (engineRetention: the flow's override, else the customer's)
// with the engine's grace, rounded up to whole days.
func subjectRetentionDays(customer Customer, override time.Duration) int {
	day := hoursPerDay * time.Hour
	return int((engineRetention(&customer, override) + day - 1) / day)
}

// requestTenant is the tenant req's session runs under.
func requestTenant(req Request) proofingprovider.Tenant {
	return orgTenant(req.OrganizationID)
}

// Flows returns the org's active flows with the admin's selection applied:
// every flow for FlowsAll, else only the ones members may send a request on.
func (s *Service) Flows(ctx context.Context, org Org, view FlowView) ([]OrgFlow, error) {
	tenant := orgTenant(org.ID)
	return s.orgFlows(ctx, org, tenant, view)
}

// orgFlows is Flows with the org's engine key already resolved.
func (s *Service) orgFlows(ctx context.Context, org Org, tenant proofingprovider.Tenant, view FlowView) ([]OrgFlow, error) {
	flows, err := s.ips.ListFlows(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	sel, err := s.settings.FlowSelection(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	diplomas, err := s.allFlowDiplomas(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	kinds, err := s.allFlowKinds(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgFlow, 0, len(flows))
	for _, f := range flows {
		of := OrgFlow{
			Flow: f, Allowed: slices.Contains(sel.FlowIDs, f.ID) && flowCompletable(f), Default: f.ID == sel.DefaultFlowID,
			Diplomas: DiplomasOff, Kind: FlowIdentity,
		}
		if kind, ok := kinds[f.ID]; ok {
			of.Kind = kind
		}
		if mode, ok := diplomas[f.ID]; ok {
			of.Diplomas = mode
		}
		if view == FlowsAll || of.Allowed {
			out = append(out, of)
		}
	}
	return out, nil
}

// ConfigureFlows replaces the org's allow-list. Every chosen flow must be an
// active the engine flow a recipient can finish, and a non-empty selection needs a
// default among it.
func (s *Service) ConfigureFlows(ctx context.Context, org Org, sel FlowSelection) error {
	sel, err := s.validSelection(ctx, org, sel, flowCompletable)
	if err != nil {
		return err
	}
	if err := s.checkMemberFlowKinds(ctx, org.ID, sel.FlowIDs); err != nil {
		return err
	}
	return s.settings.SaveFlowSelection(ctx, org.ID, sel)
}

// validSelection deduplicates sel and checks it: every flow an active the engine flow
// of the org a recipient can finish, and a non-empty selection has its default
// among it. Both the members' allow-list and a customer's assignment are one;
// completable is what the recipients can finish (flowCompletable for members,
// customerCompletable for a customer's subjects).
func (s *Service) validSelection(ctx context.Context, org Org, sel FlowSelection, completable func(proofingprovider.Flow) bool) (FlowSelection, error) {
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
	flows, err := s.Flows(ctx, org, FlowsAll)
	if err != nil {
		return sel, err
	}
	for _, id := range ids {
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == id })
		if i < 0 {
			return sel, ErrFlowNotFound
		}
		if !completable(flows[i].Flow) {
			return sel, ErrFlowNotCompletable
		}
	}
	return sel, nil
}

// CreateFlow defines a flow in the engine (version 1) and audits it. The engine validates the
// combination of steps, checks and assurance level; its refusal comes back as a
// *proofingprovider.RejectedError.
func (s *Service) CreateFlow(ctx context.Context, org Org, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	in, err := normalizeFlow(in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	tenant := orgTenant(org.ID)
	flow, err := s.ips.CreateFlow(ctx, tenant, in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowCreated, flow); err != nil {
		return proofingprovider.Flow{}, err
	}
	return flow, nil
}

// EditFlow saves in as the next version of flow id. The engine makes it the active
// version at once; requests already sent keep the version their session pinned.
func (s *Service) EditFlow(ctx context.Context, org Org, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	in, err := normalizeFlow(in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	tenant := orgTenant(org.ID)
	flow, err := s.ips.CreateFlowVersion(ctx, tenant, id, in)
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
	tenant := orgTenant(org.ID)
	versions, err := s.ips.ListFlowVersions(ctx, tenant, id)
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
	tenant := orgTenant(org.ID)
	flow, err := s.ips.ActivateFlowVersion(ctx, tenant, id, version)
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
// face capture to the app: a recipient only has vcmrtd/idem (the engine has no end-user
// web page yet), while the engine's own default is the browser.
func normalizeFlow(in proofingprovider.FlowSpec) (proofingprovider.FlowSpec, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Steps) == 0 {
		return in, fmt.Errorf("%w: a flow needs a name and at least one step", ErrInvalidInput)
	}
	in.SelfieLocation = ""
	if !hasFaceStep(in.Steps) {
		in.FaceProvider = ""
		return in, nil
	}
	in.SelfieLocation = selfieLocationNative
	if in.FaceProvider == "" {
		in.FaceProvider = faceProviderRegula
	}
	if !slices.Contains(faceProviders, in.FaceProvider) {
		return in, fmt.Errorf("%w: unknown face provider %q", ErrInvalidInput, in.FaceProvider)
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
	// DeepLink is the session's vcmrtd link, for an API caller to show itself;
	// DeepLinkExpiresAt is when it can no longer be scanned.
	DeepLink          string
	DeepLinkExpiresAt time.Time
	// HostedURL is a hosted request's link to its public page.
	HostedURL string
}

// CreateRequest sends a proofing request: to a member, on one of the flows the
// org admin made available to members, or for a customer to its subject, on one
// of the flows assigned to that customer. It creates the request's engine session
// for the chosen app, stores the request with it, and, for a mailed request,
// mails the subject the session's vcmrtd deep link: SessionTTL runs from the
// send. An on-screen request is created only once the subject accepted what is
// collected, so its session starts then. A session whose request then fails to
// store is erased again (discardSession).
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
	tenant := orgTenant(org.ID)
	flow, err := s.sendableFlow(ctx, org, tenant, in.FlowID, customer)
	if err != nil {
		return Sent{}, err
	}
	if in.Method == proofingprovider.MethodYivi && !yiviAppAvailable(flow) {
		return Sent{}, fmt.Errorf("%w: this flow's face provider only runs in the Idem app", ErrInvalidInput)
	}
	if subject.BirthDate != "" && !readsIdentity(flow) {
		return Sent{}, fmt.Errorf("%w: this flow does not read the name and date of birth, so it cannot check for one person", ErrInvalidInput)
	}
	photo, err := referencePhotoFor(flow, in.ReferencePhoto)
	if err != nil {
		return Sent{}, err
	}
	diplomas, err := s.flowDiplomas(ctx, org.ID, flow.ID)
	if err != nil {
		return Sent{}, err
	}
	kind, err := s.flowKind(ctx, org.ID, flow.ID)
	if err != nil {
		return Sent{}, err
	}
	// A data request is a customer's subject asking for their data; it
	// collects no diplomas.
	if kind.dataRequest() {
		if customer == nil {
			return Sent{}, ErrDataFlowForMember
		}
		diplomas = DiplomasOff
	}
	if diplomas.asked() && in.Channel != ChannelHosted && in.Channel != ChannelOnScreen {
		return Sent{}, ErrDiplomasNeedPage
	}
	if in.Channel == ChannelHosted {
		return s.createHosted(ctx, org, by, in, subject, *customer, flow, diplomas, kind, photo)
	}

	id := uuid.New()
	input := proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: id.String(), TTL: ttl, Method: in.Method,
		Language: string(in.Language), Retention: engineRetention(customer, time.Duration(flow.RetentionOverrideSeconds)*time.Second), ReferencePhoto: photo,
	}
	sess, err := s.ips.CreateSession(ctx, tenant, input)
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: %w", id, err)
	}
	// An Idem session is the vcmrtd link; a Yivi one is started from the screen.
	if in.Method == proofingprovider.MethodIdem && sess.Claim == nil {
		return Sent{}, s.discardSession(ctx, tenant, sess,
			fmt.Errorf("proofing: create session request %s: IPS offered no vcmrtd link", id))
	}
	req, err := s.requests.Create(ctx, NewStoredRequest{
		ID: id, OrgID: org.ID, RequestedBy: by.userID(), APIKeyID: by.APIKeyID, Subject: subject,
		Flow: flow, LinkExpiresAt: sess.ExpiresAt, Method: in.Method, Channel: in.Channel,
		Diplomas: diplomas, FlowKind: kind,
	})
	if err != nil {
		return Sent{}, s.discardSession(ctx, tenant, sess, err)
	}
	attached, err := s.requests.AttachSession(ctx, req, sess)
	if err != nil {
		return Sent{}, s.discardSession(ctx, tenant, sess, err)
	}
	if !attached {
		return Sent{}, s.discardSession(ctx, tenant, sess,
			fmt.Errorf("proofing: attach session request %s: the new request moved on", id))
	}
	if sess.FlowVersion != 0 {
		req.FlowVersion = sess.FlowVersion
	}
	req.session = &ipsSession{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt}
	out := Sent{Request: req}
	if sess.Claim == nil {
		return out, nil
	}
	out.DeepLink, out.DeepLinkExpiresAt = sess.Claim.DeepLink, sess.Claim.ExpiresAt
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

// discardSession erases sess, an engine session no request holds (its
// request failed to store, or to attach it): it may carry the reference
// photo. It runs to the end even when ctx was cancelled (a client timeout is
// one way to get here). Its own failure is logged and joined to cause, which
// it returns.
func (s *Service) discardSession(ctx context.Context, tenant proofingprovider.Tenant, sess proofingprovider.Session, cause error) error {
	err := s.ips.DeleteSession(context.WithoutCancel(ctx), tenant, sess.ID, sess.Token)
	if err == nil {
		return cause
	}
	slog.ErrorContext(ctx, "identity proofing: unattached engine session not erased",
		slog.String("session_id", sess.ID), slog.Any("error", err))
	return errors.Join(cause, fmt.Errorf("proofing: erase unattached session %s: %w", sess.ID, err))
}

// createHosted stores a hosted request with its link and no the engine session yet:
// the subject starts one from the page (StartHosted) until hostedLinkTTL. Its
// redirect must be on one of the customer's allowed origins.
func (s *Service) createHosted(ctx context.Context, org Org, by Requester, in NewRequest, subject Subject,
	customer Customer, flow proofingprovider.Flow, diplomas DiplomaMode, kind FlowKind, photo *proofingprovider.Image,
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
		Flow: flow, LinkExpiresAt: s.now().Add(hostedLinkTTL), Method: in.Method, Channel: in.Channel,
		LinkTokenHash: hash, RedirectURL: in.RedirectURL, Language: in.Language,
		Diplomas: diplomas, FlowKind: kind, ReferencePhoto: photo,
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
		if strings.TrimSpace(in.SubjectBirthDate) != "" {
			return Subject{}, nil, fmt.Errorf("%w: only a customer's request can be for one known person", ErrInvalidInput)
		}
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
	if !customer.HasAPIKey {
		return Subject{}, nil, ErrCustomerNoAPIKey
	}
	name := strings.TrimSpace(in.SubjectName)
	if len(name) > maxSubjectNameLength {
		return Subject{}, nil, fmt.Errorf("%w: the name is too long", ErrInvalidInput)
	}
	birthDate, err := expectedBirthDate(in.SubjectBirthDate, name, s.now())
	if err != nil {
		return Subject{}, nil, err
	}
	subject := Subject{CustomerID: in.CustomerID, Name: name, BirthDate: birthDate}
	// No address is needed when nothing is mailed: on screen, a hosted link,
	// or a session the caller shows its own QR for (SkipMail).
	if (in.Channel != ChannelEmail || in.SkipMail) && strings.TrimSpace(in.SubjectEmail) == "" {
		return subject, &customer, nil
	}
	email, err := user.ParseEmail(in.SubjectEmail)
	if err != nil {
		return Subject{}, nil, fmt.Errorf("%w: enter a valid e-mail address", ErrInvalidInput)
	}
	subject.Email = string(email)
	return subject, &customer, nil
}

// referencePhotoFor checks the reference photo a request on flow carries: one
// is required exactly when the flow flowNeedsReferencePhoto, so a chip flow is
// never matched against anything but the chip. It must be a PNG, JPEG or WebP
// image (sniffed, not trusted from the caller) of at most
// maxReferencePhotoBytes; nil for a flow that takes none.
func referencePhotoFor(flow proofingprovider.Flow, raw string) (*proofingprovider.Image, error) {
	raw = strings.TrimSpace(raw)
	switch needs := flowNeedsReferencePhoto(flow); {
	case needs && raw == "":
		return nil, ErrReferencePhotoRequired
	case !needs && raw != "":
		return nil, fmt.Errorf("%w: this flow matches the face against the document's chip and takes no reference photo", ErrInvalidInput)
	case !needs:
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: the reference photo is not standard base64", ErrInvalidInput)
	}
	if len(data) > maxReferencePhotoBytes {
		return nil, fmt.Errorf("%w: the reference photo is larger than %d KiB", ErrInvalidInput, maxReferencePhotoBytes>>10)
	}
	mime := http.DetectContentType(data)
	if !slices.Contains(referencePhotoTypes, mime) {
		return nil, fmt.Errorf("%w: the reference photo must be a PNG, JPEG or WebP image", ErrInvalidInput)
	}
	return &proofingprovider.Image{MimeType: mime, Base64: base64.StdEncoding.EncodeToString(data)}, nil
}

// expectedBirthDate checks the birth date a request for one known person is
// sent with (YYYY-MM-DD, in the past) and returns it in that form; empty is a
// request for anyone. The person is the name sent with it, so it needs one.
func expectedBirthDate(raw, name string, now time.Time) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if name == "" {
		return "", fmt.Errorf("%w: a request for one person needs their name with the date of birth", ErrInvalidInput)
	}
	date, err := time.Parse(birthDateLayout, raw)
	if err != nil || !date.Before(now) {
		return "", fmt.Errorf("%w: the date of birth is a past date written YYYY-MM-DD", ErrInvalidInput)
	}
	return date.Format(birthDateLayout), nil
}

// readsIdentity reports whether flow f's result carries the holder's name and
// date of birth (the document data, dg1): what a request for one known person
// is matched against. The engine hands a flow back listing what it releases.
func readsIdentity(f proofingprovider.Flow) bool {
	return slices.Contains(f.RequestedAttributes, attributeDocument)
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
func (s *Service) sendableFlow(ctx context.Context, org Org, tenant proofingprovider.Tenant, flowID string, customer *Customer) (proofingprovider.Flow, error) {
	if customer == nil {
		flows, err := s.orgFlows(ctx, org, tenant, FlowsAll)
		if err != nil {
			return proofingprovider.Flow{}, err
		}
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == flowID })
		if i < 0 {
			return proofingprovider.Flow{}, ErrFlowNotFound
		}
		if !flowCompletable(flows[i].Flow) {
			return proofingprovider.Flow{}, ErrFlowNotCompletable
		}
		if !flows[i].Allowed {
			return proofingprovider.Flow{}, ErrFlowNotAllowed
		}
		return flows[i].Flow, nil
	}
	if flowID == "" {
		// An API caller that names no flow gets the customer's default.
		if flowID = customer.Flows.DefaultFlowID; flowID == "" {
			return proofingprovider.Flow{}, ErrFlowNotAssigned
		}
	}
	flows, err := s.ips.ListFlows(ctx, tenant)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	i := slices.IndexFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == flowID })
	if i < 0 {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	if !customerCompletable(flows[i]) {
		return proofingprovider.Flow{}, ErrFlowNotCompletable
	}
	if !slices.Contains(customer.Flows.FlowIDs, flowID) {
		return proofingprovider.Flow{}, ErrFlowNotAssigned
	}
	return flows[i], nil
}

// Requests lists the org's requests narrowed by filter, as stored: outcomes
// land by the engine's notice and the deadline job, so a list read never calls the engine.
func (s *Service) Requests(ctx context.Context, orgID uuid.UUID, filter RequestFilter) ([]Request, error) {
	return s.requests.List(ctx, orgID, filter)
}

// reconcile reads the request's engine session and records what the engine decided,
// with no actor: the audit trail shows it as the system's. An the engine
// failure is logged and the row is shown as last known: a read must not fail
// because the engine is briefly away.
func (s *Service) reconcile(ctx context.Context, tenant proofingprovider.Tenant, req Request) Request {
	req, _ = s.tryReconcile(ctx, tenant, req)
	return req
}

// tryReconcile is reconcile that also returns the (already logged) failure.
func (s *Service) tryReconcile(ctx context.Context, tenant proofingprovider.Tenant, req Request) (Request, error) {
	// What the engine reports is the subject's doing and the wallet's record of it, not
	// that of whoever's read happened to trigger the check.
	ctx = audit.WithoutActor(ctx)
	sess := req.session
	res, err := s.ips.SessionStatus(ctx, tenant, sess.ID, sess.Token)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		// A data request the wallet holds in review, or decided, outlives
		// the engine's copy: the person was proven, and the review is the
		// wallet's own.
		if req.FlowKind.dataRequest() && req.Status.Settled() {
			return req, nil
		}
		// The engine purged or erased the session: treat it like a lapsed one.
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
		if err := s.requests.MarkStarted(subjectAppContext(ctx, methodOr(res.Method, req.Method)), req, sess.ID, res.Method); err != nil {
			slog.WarnContext(ctx, "identity proofing: mark started failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		req.Status = StatusInProgress
		req.Method = methodOr(res.Method, req.Method)
		return req, nil
	case proofingprovider.StatusExpired, proofingprovider.StatusCancelled:
		// needs_review is still open in the engine, so it can end there too.
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
	if next == StatusApproved && req.ExpectsSubject {
		if next, res, err = s.matchSubject(ctx, tenant, req, res); err != nil {
			slog.WarnContext(ctx, "identity proofing: read the identity to match failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
	}
	if req.FlowKind.dataRequest() {
		// A data request's decision is the wallet reviewer's alone, whatever
		// the engine reports after it.
		if req.Status == StatusApproved || req.Status == StatusRejected {
			return req, nil
		}
		// A proven person's data request goes to review with the customer's
		// sessions of that person, found once.
		if next == StatusApproved || next == StatusNeedsReview {
			if req.Status != StatusNeedsReview {
				if err := s.findDataMatches(ctx, tenant, req); err != nil {
					slog.WarnContext(ctx, "identity proofing: find data request matches failed",
						slog.String("request_id", req.ID.String()), slog.Any("error", err))
					return req, err
				}
			}
			next = StatusNeedsReview
		}
	}
	if next == req.Status {
		return req, nil
	}
	// The name read off the document is kept only for a customer's subject the
	// sender may know only by address, and only once the document is approved
	// (or, for a data request, the person is proven): only then is the full
	// result, with its personal data, read at all.
	if req.CustomerID != nil && (next == StatusApproved || (req.FlowKind.dataRequest() && next == StatusNeedsReview)) {
		full, err := s.ips.SessionResult(ctx, tenant, sess.ID, sess.Token)
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: read the approved result failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		res.Name = full.Name
	}
	// The outcome is the app's doing, from the evidence it sent; one decided
	// after review is the reviewer's, audited as review_decided.
	outcomeCtx := ctx
	if req.Status != StatusNeedsReview {
		outcomeCtx = subjectAppContext(ctx, methodOr(res.Method, req.Method))
	}
	if err := s.requests.RecordOutcome(outcomeCtx, req, sess.ID, next, res); err != nil {
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
	if next != StatusNeedsReview {
		return req, nil
	}
	// A paused org can decide nothing, so a review it reaches is rejected at once.
	decided, err := s.rejectIfPaused(ctx, req)
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: reject the review of a paused org failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req, err
	}
	return decided, nil
}

// enforceAssurance holds an approval the engine reported to the level the
// request's flow demanded at send. The engine's Idem flow already rejects a
// session below its flow's level; the Yivi method's sessions are approved
// without that comparison. An approval that falls short is a rejection
// with errorAssuranceNotMet. A Yivi session an older the engine did not score counts
// as yiviEIDASLevel.
func enforceAssurance(req Request, res proofingprovider.Result) (Status, proofingprovider.Result) {
	if res.EIDASLevel == "" && methodOr(res.Method, req.Method) == proofingprovider.MethodYivi {
		res.EIDASLevel = yiviEIDASLevel
	}
	if meetsAssurance(res.EIDASLevel, req.RequiredAssuranceLevel) {
		return StatusApproved, res
	}
	res.ErrorCode = errorAssuranceNotMet
	return StatusRejected, res
}

// matchSubject holds an approval of a request for one known person to that
// person: the identity read off the document must be the request's full name,
// every word in order (samePerson), born on its birth date, or the request is
// rejected with errorIdentityMismatch. A request
// already decided holds no birth date any more, and is never matched again.
func (s *Service) matchSubject(ctx context.Context, tenant proofingprovider.Tenant, req Request, res proofingprovider.Result) (Status, proofingprovider.Result, error) {
	if req.expectedBirthDate == "" {
		return StatusApproved, res, nil
	}
	identity, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil {
		return "", res, fmt.Errorf("proofing: identity to match request %s: %w", req.ID, err)
	}
	if samePerson(identity.GivenName, identity.FamilyName, identity.BirthDate, req.SubjectName, req.expectedBirthDate) {
		return StatusApproved, res, nil
	}
	res.ErrorCode = errorIdentityMismatch
	return StatusRejected, res, nil
}

// SubjectAppActorPrefix starts the actor label of the app a subject proofed
// with (`app:idem_app`, `app:yivi_app`, `app:browser`): what that app caused is
// audited as its doing, not the system's.
const SubjectAppActorPrefix = "app:"

// subjectAppContext makes the subject's app the actor of what ctx records
// next; with no app known it leaves ctx as it is.
func subjectAppContext(ctx context.Context, method proofingprovider.Method) context.Context {
	if method == "" {
		return ctx
	}
	return audit.ContextWithActor(ctx, audit.Actor{Label: SubjectAppActorPrefix + string(method)})
}

// methodOr is the method the engine reported, or the one already known: a later read
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
func (s *Service) CustomerFlows(ctx context.Context, org Org, id uuid.UUID, view FlowView) ([]CustomerFlow, error) {
	customer, err := s.customers.Get(ctx, org.ID, id)
	if err != nil {
		return nil, err
	}
	flows, err := s.Flows(ctx, org, FlowsAll)
	if err != nil {
		return nil, err
	}
	out := make([]CustomerFlow, 0, len(flows))
	for _, f := range flows {
		cf := CustomerFlow{
			Flow:     f.Flow,
			Assigned: slices.Contains(customer.Flows.FlowIDs, f.ID) && customerCompletable(f.Flow),
			Default:  f.ID == customer.Flows.DefaultFlowID,
			Diplomas: f.Diplomas,
			Kind:     f.Kind,
			RetentionDays: subjectRetentionDays(customer,
				time.Duration(f.RetentionOverrideSeconds)*time.Second),
		}
		if view == FlowsAll || cf.Assigned {
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

// SetCustomerStatus pauses or resumes proofing for a customer: while paused,
// no request can be sent for it. One with a session waiting for review is
// ErrCustomerHasOpenReviews, checked with the pause in one transaction.
func (s *Service) SetCustomerStatus(ctx context.Context, orgID, id uuid.UUID, status CustomerStatus) (Customer, error) {
	if status != CustomerActive && status != CustomerPaused {
		return Customer{}, fmt.Errorf("%w: a customer is active or paused", ErrInvalidInput)
	}
	return s.customers.SetStatus(ctx, orgID, id, status)
}

// CustomerRequest is one of a customer's requests; see readReconciled.
func (s *Service) CustomerRequest(ctx context.Context, scope CustomerScope, id uuid.UUID) (Request, error) {
	req, err := s.requests.GetForCustomer(ctx, scope, id)
	if err != nil {
		return Request{}, err
	}
	return s.readReconciled(ctx, req)
}

// StoredCustomerRequest is one of a customer's requests as stored, without
// re-checking the engine: what a headless poll answers from.
func (s *Service) StoredCustomerRequest(ctx context.Context, scope CustomerScope, id uuid.UUID) (Request, error) {
	return s.requests.GetForCustomer(ctx, scope, id)
}

// CustomerRequestPage is a page of a customer's requests, newest first, from
// stored state (no the engine call), and the cursor of the next page ("" at the end).
// A cursor that does not decode is ErrInvalidInput.
func (s *Service) CustomerRequestPage(ctx context.Context, scope CustomerScope, cursor string, limit int) ([]Request, string, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	var after *RequestCursor
	if cursor != "" {
		c, ok := decodeRequestCursor(cursor)
		if !ok {
			return nil, "", fmt.Errorf("%w: the cursor is not one this API gave", ErrInvalidInput)
		}
		after = &c
	}
	// One extra row tells whether a next page exists.
	reqs, err := s.requests.ListPage(ctx, scope, after, limit+1)
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

// CancelRequest ends a customer's request that has no outcome yet, in the engine too,
// and marks it cancelled. One that has an outcome or ended is ErrSessionOver.
func (s *Service) CancelRequest(ctx context.Context, scope CustomerScope, id uuid.UUID) (Request, error) {
	req, err := s.CustomerRequest(ctx, scope, id)
	if err != nil {
		return Request{}, err
	}
	err = s.cancelOnce(ctx, req)
	if errors.Is(err, errCancelSessionMoved) {
		// A hosted start raced the cancel; a session is attached once, so the
		// second read holds the last one.
		if req, err = s.requests.Get(ctx, req.OrganizationID, req.ID); err != nil {
			return Request{}, err
		}
		err = s.cancelOnce(ctx, req)
	}
	if err != nil {
		return Request{}, err
	}
	return s.requests.GetForCustomer(ctx, scope, id)
}

// cancelOnce ends req's live engine session and marks req cancelled.
func (s *Service) cancelOnce(ctx context.Context, req Request) error {
	if st := req.EffectiveStatus(s.now()); st != StatusPending && st != StatusInProgress || req.PurgedAt != nil {
		return ErrSessionOver
	}
	if sess := req.liveSession(s.now()); sess != nil {
		tenant := requestTenant(req)
		if err := s.ips.CancelSession(ctx, tenant, sess.ID, sess.Token); err != nil {
			var rejected *proofingprovider.RejectedError
			if errors.As(err, &rejected) {
				// The engine decided it first; the notified outcome records that.
				return ErrSessionOver
			}
			return fmt.Errorf("proofing: cancel request %s: %w", req.ID, err)
		}
	}
	cancelled, err := s.requests.Cancel(ctx, req)
	if err != nil {
		return err
	}
	if !cancelled {
		return ErrSessionOver
	}
	return nil
}

// RequestResult reads a settled customer request's result from the engine, the
// wallet storing no identity, and audits identity_proofing.result_read. One
// not settled yet is ErrResultNotReady; one erased or gone in the engine is not found.
func (s *Service) RequestResult(ctx context.Context, scope CustomerScope, id uuid.UUID) (Request, proofingprovider.Identity, error) {
	req, err := s.CustomerRequest(ctx, scope, id)
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
	tenant := requestTenant(req)
	identity, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if errors.Is(err, proofingprovider.ErrNotFound) {
		return Request{}, proofingprovider.Identity{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, proofingprovider.Identity{}, fmt.Errorf("proofing: result request %s: %w", req.ID, err)
	}
	if err := s.requests.RecordResultRead(ctx, req); err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	return req, heldToVerdict(req, identity), nil
}

// heldToVerdict holds the engine's identity to the wallet's verdict on req: an
// approval the wallet rejected (errorAssuranceNotMet, errorIdentityMismatch)
// is read as that rejection, without the person or their images. A mismatch
// is someone other than the expected person, whose identity is never shown.
func heldToVerdict(req Request, identity proofingprovider.Identity) proofingprovider.Identity {
	if identity.Status != proofingprovider.StatusApproved || req.Status == StatusApproved ||
		(req.FlowKind.dataRequest() && req.Status == StatusNeedsReview) {
		return identity
	}
	return proofingprovider.Identity{
		Result: proofingprovider.Result{
			Method: identity.Method, Status: proofingprovider.StatusRejected, ErrorCode: req.ErrorCode,
			AssuranceLevel: identity.AssuranceLevel, EIDASLevel: identity.EIDASLevel, CompletedAt: identity.CompletedAt,
		},
		Evidence: identity.Evidence,
	}
}

// PurgeRequest erases a customer's request in the engine and every personal detail
// the wallet holds of it, whatever its state; the row stays, marked purged.
func (s *Service) PurgeRequest(ctx context.Context, scope CustomerScope, id uuid.UUID) error {
	req, err := s.requests.GetForCustomer(ctx, scope, id)
	if err != nil {
		return err
	}
	if req.PurgedAt != nil {
		return nil
	}
	return s.purge(ctx, req)
}

// purgeBatch is how many overdue requests PurgeDue takes at a time.
const purgeBatch = 100

// PurgeDue purges every request past its customer's retention, for the
// pruner. One that the engine fails to erase is left for the next run. It
// first rejects the reviews left open in a paused org (rejectPausedReviews),
// whose failure is reported once the purge ran.
func (s *Service) PurgeDue(ctx context.Context) (int64, error) {
	sweepErr := s.rejectPausedReviews(ctx)
	purged, err := s.purgeDue(ctx)
	return purged, errors.Join(err, sweepErr)
}

// purgeDue is PurgeDue's purge.
func (s *Service) purgeDue(ctx context.Context) (int64, error) {
	// A proofed name goes at its own retention, whether or not the request
	// is due yet (one still under review is not).
	if _, err := s.requests.ClearExpiredProofedNames(ctx); err != nil {
		return 0, err
	}
	var purged int64
	for {
		due, err := s.requests.ListPurgeDue(ctx, purgeBatch)
		if err != nil {
			return purged, err
		}
		failed := 0
		for _, req := range due {
			if err := s.lapseReview(ctx, req); err != nil {
				failed++
				slog.WarnContext(ctx, "identity proofing: lapse review failed",
					slog.String("request_id", req.ID.String()), slog.Any("error", err))
				continue
			}
			if err := s.purge(ctx, req); err != nil {
				failed++
				slog.WarnContext(ctx, "identity proofing: purge failed",
					slog.String("request_id", req.ID.String()), slog.Any("error", err))
				continue
			}
			purged++
		}
		// A full batch of failures would come back as is: wait for the next run.
		if len(due) < purgeBatch || failed == len(due) {
			return purged, nil
		}
	}
}

// errorReviewLapsed is the error code of a data request nobody reviewed in
// time: dataRequestReviewDays after it went to review (purge_at,
// refreshPurgeAt).
const errorReviewLapsed = "REVIEW_LAPSED"

// lapseReview rejects a data request whose review is due for purge: nobody
// decided it in time, and it is not left purged in review. Any other request
// is left as it is.
func (s *Service) lapseReview(ctx context.Context, req Request) error {
	if !req.FlowKind.dataRequest() || req.Status != StatusNeedsReview || req.session == nil {
		return nil
	}
	return s.requests.RecordOutcome(ctx, req, req.session.ID, StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorReviewLapsed})
}

// purge erases req's session in the engine, then what the wallet holds of it.
// A session attached since req was read (a hosted start racing the purge) is
// read back and erased too, before the row is purged.
func (s *Service) purge(ctx context.Context, req Request) error {
	if err := s.purgeOnce(ctx, req); !errors.Is(err, errPurgeSessionMoved) {
		return err
	}
	fresh, err := s.requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil {
		return err
	}
	// A session is attached once, so the second read holds the last one.
	return s.purgeOnce(ctx, fresh)
}

func (s *Service) purgeOnce(ctx context.Context, req Request) error {
	if req.session != nil {
		if err := s.ips.DeleteSession(ctx, requestTenant(req), req.session.ID, req.session.Token); err != nil {
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

// readReconciled re-checks a request that may have moved on in the engine, at most
// once per readReconcileEvery: a fallback for a missed notice, so a poller
// never turns into one the engine read per poll.
func (s *Service) readReconciled(ctx context.Context, req Request) (Request, error) {
	if !req.needsReconcile() || !s.readChecks.allow(req.ID, s.now()) {
		return req, nil
	}
	tenant := requestTenant(req)
	return s.reconcile(ctx, tenant, req), nil
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

// yiviSession is the running the engine session of a request created for the Yivi app.
func (s *Service) yiviSession(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (Request, *ipsSession, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return Request{}, nil, err
	}
	return s.yiviSessionOf(req)
}

// yiviSessionOf is req's running the engine session, if req is for the Yivi app.
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

// App is where a running Idem request's phone is, read live from the engine: what the
// on-screen page polls to hide its QR once scanned and show one once the app left.
func (s *Service) App(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID) (proofingprovider.App, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return "", err
	}
	if req.Method != proofingprovider.MethodIdem {
		return "", ErrWrongMethod
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return "", ErrSessionOver
	}
	tenant := requestTenant(req)
	res, err := s.ips.SessionStatus(ctx, tenant, sess.ID, sess.Token)
	if err != nil {
		return "", fmt.Errorf("proofing: app of request %s: %w", req.ID, err)
	}
	return res.App, nil
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
	if req.Method != proofingprovider.MethodIdem {
		return proofingprovider.Claim{}, ErrWrongMethod
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return proofingprovider.Claim{}, ErrSessionOver
	}
	tenant := requestTenant(req)
	claim, err := s.ips.SessionHandover(ctx, tenant, sess.ID, sess.Token)
	var rejected *proofingprovider.RejectedError
	switch {
	case errors.As(err, &rejected) && rejected.Code == proofingprovider.CodeDeviceActive:
		return proofingprovider.Claim{}, ErrDeviceActive
	case errors.As(err, &rejected):
		return proofingprovider.Claim{}, ErrSessionOver
	case err != nil:
		return proofingprovider.Claim{}, fmt.Errorf("proofing: claim link request %s: %w", req.ID, err)
	}
	// A fresh claim for a phone that never scanned is no handover.
	if claim.Handover {
		if err := s.requests.RecordHandover(ctx, req, claim.ExpiresAt); err != nil {
			return proofingprovider.Claim{}, err
		}
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

// YiviDisclosure hands the subject's finished disclosure to the engine as the face
// check's reference, or answers ErrDisclosurePending while the subject has not
// finished in the Yivi app. A disclosure the engine cannot use (no photo, no face in
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
	tenant := requestTenant(req)
	attributes := maps.Clone(doc.Claims)
	delete(attributes, openid4vpverifier.ClaimPhoto)
	disclosure, err := s.ips.SubmitReference(ctx, tenant, sess.ID, sess.Token, proofingprovider.Reference{
		Credential: doc.Credential, Photo: doc.Claims[openid4vpverifier.ClaimPhoto], Attributes: attributes,
	})
	if err != nil {
		return proofingprovider.YiviDisclosure{}, yiviStepError(err)
	}
	s.reconcile(ctx, tenant, req)
	return disclosure, nil
}

// FaceFrame scores one live camera frame of an on-screen Yivi request against
// the disclosed photo. The frame passes through to the engine and is not kept; a
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

// yiviStepError reads the engine no longer knowing the session (purged, or the stub
// restarted) or answering it gone as the session being over; the request's
// row catches up on the next reconcile.
func yiviStepError(err error) error {
	var rejected *proofingprovider.RejectedError
	if errors.Is(err, proofingprovider.ErrNotFound) || (errors.As(err, &rejected) && rejected.Status == http.StatusGone) {
		return fmt.Errorf("%w: %w", ErrSessionOver, err)
	}
	return err
}

// reconcileNow records what the engine decided for req. A failure is logged: the
// background reconciler picks the outcome up later.
func (s *Service) reconcileNow(ctx context.Context, req Request) {
	s.reconcile(ctx, requestTenant(req), req)
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

// CreateAPIKey adds a key with every scope to a customer; the returned secret
// is shown once.
func (s *Service) CreateAPIKey(ctx context.Context, orgID, customerID, createdBy uuid.UUID, name string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxAPIKeyNameLength {
		return APIKey{}, "", fmt.Errorf("%w: an API key needs a name of at most %d characters", ErrInvalidInput, maxAPIKeyNameLength)
	}
	return s.apiKeys.Create(ctx, orgID, customerID, createdBy, name, slices.Clone(APIKeyScopes))
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

// removeCustomerAttempts is how often RemoveCustomer purges and tries again
// when requests keep being sent for the customer meanwhile.
const removeCustomerAttempts = 3

// RemoveCustomer deletes a customer and every request sent for it. Each
// request is erased as a purge erases it first: its engine session, and the
// subject's name, e-mail and diplomas in its audit trail, which stays once
// the rows are gone. The removal itself refuses while a request is left
// unpurged (one sent meanwhile), so this purges again, up to
// removeCustomerAttempts times, then answers ErrCustomerSessionsLeft.
func (s *Service) RemoveCustomer(ctx context.Context, orgID, id uuid.UUID) error {
	if _, err := s.customers.Get(ctx, orgID, id); err != nil {
		return err
	}
	var err error
	for range removeCustomerAttempts {
		reqs, listErr := s.requests.ListUnpurgedForCustomer(ctx, orgID, id)
		if listErr != nil {
			return listErr
		}
		for _, req := range reqs {
			if err := s.purge(ctx, req); err != nil {
				return err
			}
		}
		if err = s.customers.Remove(ctx, orgID, id); !errors.Is(err, ErrCustomerSessionsLeft) {
			return err
		}
	}
	return err
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

// Stats counts the customer requests sent within statsWindow, per customer and
// flow, narrowed to one member's when requestedBy is set.
func (s *Service) Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]StatsRow, time.Time, error) {
	since := s.now().Add(-statsWindow)
	rows, err := s.requests.Stats(ctx, orgID, requestedBy, since)
	return rows, since, err
}

// AssignCustomerFlows replaces the flows assigned to a customer. Any flow of the
// org a recipient can finish may be assigned, whether or not members may use it.
func (s *Service) AssignCustomerFlows(ctx context.Context, org Org, id uuid.UUID, sel FlowSelection) (Customer, error) {
	if _, err := s.customers.Get(ctx, org.ID, id); err != nil {
		return Customer{}, err
	}
	sel, err := s.validSelection(ctx, org, sel, customerCompletable)
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
