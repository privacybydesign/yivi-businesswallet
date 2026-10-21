package proofing

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
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
	DecideReview(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string, d proofingprovider.ReviewDecision) error
	SessionHandover(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Claim, error)
	SessionIdentity(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) (proofingprovider.Identity, error)
	CancelSession(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) error
	DeleteSession(ctx context.Context, t proofingprovider.Tenant, sessionID, sessionToken string) error
	// SetSessionsRetention changes how long the engine keeps sessions once
	// they ended, from what each was created with (SessionInput.Retention).
	SetSessionsRetention(ctx context.Context, t proofingprovider.Tenant, sessionIDs []string, retention time.Duration) error
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
	ExtendSession(ctx context.Context, req Request, sessionID string, expiresAt time.Time) error
	EndSession(ctx context.Context, req Request, sessionID string, ipsStatus proofingprovider.Status, method proofingprovider.Method) error
	ListExternalPage(ctx context.Context, orgID uuid.UUID, after *RequestCursor, limit int) ([]Request, error)
	Cancel(ctx context.Context, req Request, endSession func(context.Context) error) (bool, error)
	RecordResultRead(ctx context.Context, req Request) error
	Purge(ctx context.Context, req Request, endSession func(context.Context) error) error
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	RecordOutcome(ctx context.Context, req Request, sessionID string, status Status, res proofingprovider.Result) error
	SetYiviTransaction(ctx context.Context, req Request, sessionID, transactionID string) error
	ReferencePhoto(ctx context.Context, req Request) (*proofingprovider.Image, error)
	Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID, since time.Time) ([]StatsRow, error)
	GetExternal(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Request, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]Request, error)
	NextDeadline(ctx context.Context, now time.Time) (time.Time, error)
	GetBySession(ctx context.Context, sessionID string) (Request, error)
	GetByLinkToken(ctx context.Context, hash []byte) (Request, error)
	LapseLinks(ctx context.Context, now time.Time, limit int) (int, error)
	RecordReviewDecision(ctx context.Context, req Request, decided Status, reason, errorCode string) error
	RecordHandover(ctx context.Context, req Request, expiresAt time.Time) error
	ListPurgeDue(ctx context.Context, limit int) ([]Request, error)
	DeferPurge(ctx context.Context, id uuid.UUID) error
	IdentitySessions(ctx context.Context, orgID uuid.UUID) (map[time.Duration][]string, error)
	HeldUntil(ctx context.Context, orgID, id uuid.UUID) (*time.Time, error)
	ClearExpiredProofedNames(ctx context.Context) (int64, error)
	ListOpenReviews(ctx context.Context, orgID uuid.UUID) ([]Request, error)
	LockReview(ctx context.Context, id uuid.UUID) (func(), error)
	AddAddress(ctx context.Context, req Request, addr Address) (bool, error)
}

// eventReader reads the audit trail (implemented by *audit.Reader).
type eventReader interface {
	ListForTarget(ctx context.Context, orgID uuid.UUID, targetType, targetID string, after *audit.Cursor, limit int) (audit.Page, error)
}

type webhookStore interface {
	Get(ctx context.Context, orgID uuid.UUID) (Webhook, error)
	Save(ctx context.Context, orgID uuid.UUID, url string, events []string) (Webhook, string, error)
	RotateSecret(ctx context.Context, orgID uuid.UUID) (Webhook, string, error)
	Remove(ctx context.Context, orgID uuid.UUID) error
	SendTest(ctx context.Context, orgID uuid.UUID) error
	Deliveries(ctx context.Context, orgID uuid.UUID) ([]Delivery, error)
	Health(ctx context.Context, orgID uuid.UUID) (WebhookHealth, error)
}

type apiKeyStore interface {
	Create(ctx context.Context, orgID, createdBy uuid.UUID, name string, scopes []string) (APIKey, string, error)
	List(ctx context.Context, orgID uuid.UUID) ([]APIKey, error)
	Revoke(ctx context.Context, orgID, id uuid.UUID) (APIKey, error)
	Authenticate(ctx context.Context, raw string) (APIKeyCaller, error)
	Touch(ctx context.Context, caller APIKeyCaller) error
}

type orgSettingsStore interface {
	Get(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Save(ctx context.Context, orgID uuid.UUID, change SessionSettingsChange, legal *LegalRetention, origins *[]string) (Settings, error)
	SaveBranding(ctx context.Context, orgID uuid.UUID, b Branding, logo LogoChange) (Settings, error)
	SyncEngineRetention(ctx context.Context, orgID uuid.UUID, sync func(context.Context, Settings) error) error
	MarkEngineRetentionStale(ctx context.Context, orgID uuid.UUID) error
	StaleEngineRetention(ctx context.Context) ([]uuid.UUID, error)
	Logo(ctx context.Context, orgID uuid.UUID) (Logo, error)
}

type pauseStore interface {
	Get(ctx context.Context, orgID uuid.UUID) (OrgPause, error)
	List(ctx context.Context) ([]OrgPause, error)
	Set(ctx context.Context, orgID uuid.UUID, state PauseState, by *uuid.UUID) (OrgPause, error)
}

type flowHostedStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowHosted, error)
	Disabled(ctx context.Context, orgID uuid.UUID) ([]string, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowHosted) (FlowHosted, error)
}

type flowBrandingStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowBranding, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowBranding, logo LogoChange) (FlowBranding, error)
	Logo(ctx context.Context, orgID uuid.UUID, flowID string) (Logo, error)
}

// Mailer sends the proofing-request e-mail (implemented by *email.Service).
type Mailer interface {
	SendIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, m email.ProofingMail) error
}

// Service orchestrates the engine, the settings and request stores, and the mailer.
type Service struct {
	settings    settingsStore
	requests    requestStore
	orgSettings orgSettingsStore
	apiKeys     apiKeyStore
	webhooks    webhookStore
	events      eventReader
	pauses      pauseStore
	// flowHostedSettings is each flow's hosted page settings; nil the defaults.
	flowHostedSettings flowHostedStore
	// flowBrandings is each flow's own branding; nil leaves every flow with
	// the org's.
	flowBrandings flowBrandingStore
	// flowDiplomaSettings is each flow's DiplomaMode, diplomas the extracts
	// requests hold; nil asks for none.
	flowDiplomaSettings flowDiplomaStore
	diplomas            diplomaStore
	diplomaChecker      diplomaChecker
	// flowAddressSettings is each flow's AddressMode; nil asks for none.
	flowAddressSettings flowAddressStore
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
	// addressLookup is the Dutch address register; nil checks no address.
	addressLookup addressFinder
}

// Stores are the service's persistence, one store per concern.
type Stores struct {
	Settings    settingsStore
	Requests    requestStore
	OrgSettings orgSettingsStore
	APIKeys     apiKeyStore
	Webhooks    webhookStore
	Events      eventReader
	// Pauses holds whether a platform admin paused an org's proofing; nil never pauses.
	Pauses pauseStore
	// FlowHosted holds each flow's hosted page settings; nil is the defaults.
	FlowHosted flowHostedStore
	// FlowBranding holds each flow's own branding; nil leaves every flow
	// with the org's.
	FlowBranding flowBrandingStore
	// FlowDiplomas holds each flow's DiplomaMode and Diplomas the extracts
	// requests hold; nil asks for none.
	FlowDiplomas flowDiplomaStore
	Diplomas     diplomaStore
	// FlowAddresses holds each flow's AddressMode; nil asks for none.
	FlowAddresses flowAddressStore
	// DataRequests holds each flow's kind and a data request's matches; nil
	// makes every flow an identity check.
	DataRequests dataRequestStore
}

// NewService builds the proofing service. A nil mailer skips the e-mail (tests).
func NewService(stores Stores, ips Provider, verifier verifier, mailer Mailer) *Service {
	return &Service{
		settings: stores.Settings, requests: stores.Requests, orgSettings: stores.OrgSettings, apiKeys: stores.APIKeys,
		webhooks: stores.Webhooks, events: stores.Events, pauses: stores.Pauses, flowHostedSettings: stores.FlowHosted,
		flowBrandings:       stores.FlowBranding,
		flowDiplomaSettings: stores.FlowDiplomas, diplomas: stores.Diplomas, dataRequests: stores.DataRequests,
		flowAddressSettings: stores.FlowAddresses,
		ips:                 ips, verifier: verifier, mailer: mailer, now: time.Now,
		readChecks: newReadThrottle(readReconcileEvery),
	}
}

// orgTenant is the org as the engine's tenant: its own id. An org needs no
// enable step before it can proof.
func orgTenant(orgID uuid.UUID) proofingprovider.Tenant {
	return proofingprovider.Tenant{ID: orgID.String()}
}

// retentionGrace is how much longer the engine keeps an external person's
// session than the org's data retention, so PurgeDue (which erases it in the
// engine too) always runs first.
const retentionGrace = 24 * time.Hour

// baseRetention is how long a session is kept once it ended, apart from the
// law: the flow's own override when it set one, else the org's data retention.
func (s Settings) baseRetention(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	return s.Session.DataRetention()
}

// engineRetention is how long the engine keeps a session once it ended: the
// flow's own override when it set one, else the org's data retention for an
// external person's request, or proofedNameRetention for a member's (settings
// nil); for an external identity check never less than the legal retention;
// plus grace: the retention setPurgeAt purges the request after.
func engineRetention(settings *Settings, kind FlowKind, override time.Duration) time.Duration {
	retention := proofedNameRetention
	if settings != nil {
		retention = settings.baseRetention(override)
	} else if override > 0 {
		retention = override
	}
	if settings != nil && kind == FlowIdentity {
		retention = max(retention, settings.LegalRetention.longest())
	}
	return retention + retentionGrace
}

// engineSessionBatch bounds how many sessions one SetSessionsRetention names.
const engineSessionBatch = 500

// syncEngineRetention gives the engine the retention of the org's unpurged
// identity checks as the settings say now (engineRetention). The engine fixes a
// session's retention when it creates it and deletes the finished one at that,
// so a check sent before the legal retention was set would lose its evidence
// long before the law allows. It runs under the settings write lock, with the
// settings read under it (OrgSettingsStore.SyncEngineRetention): two saves at
// once cannot leave the engine at the first one's retention, since the second
// sync waits for the first and reads what was saved last. The engine changes
// only what differs, so a run repeats one that failed part-way; the org stays
// marked (engine_retention_stale) until one succeeds, and the pruner retries
// it (syncStaleEngineRetention).
func (s *Service) syncEngineRetention(ctx context.Context, orgID uuid.UUID) error {
	return s.orgSettings.SyncEngineRetention(ctx, orgID, func(ctx context.Context, settings Settings) error {
		groups, err := s.requests.IdentitySessions(ctx, orgID)
		if err != nil {
			return err
		}
		tenant := orgTenant(orgID)
		for override, ids := range groups {
			retention := engineRetention(&settings, FlowIdentity, override)
			for batch := range slices.Chunk(ids, engineSessionBatch) {
				if err := s.ips.SetSessionsRetention(ctx, tenant, batch, retention); err != nil {
					return fmt.Errorf("proofing: set engine retention org %s: %w", orgID, err)
				}
			}
		}
		return nil
	})
}

// syncStaleEngineRetention syncs the engine retention of every org still
// marked for it, for the pruner: a sync that failed, or a session attached
// after one ran (markIfRetentionMoved). One org's failure leaves it marked for
// the next run and does not hold up the others.
func (s *Service) syncStaleEngineRetention(ctx context.Context) error {
	orgs, err := s.orgSettings.StaleEngineRetention(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, orgID := range orgs {
		if err := s.syncEngineRetention(ctx, orgID); err != nil {
			slog.WarnContext(ctx, "identity proofing: engine retention sync failed",
				slog.String("org_id", orgID.String()), slog.Any("error", err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// markIfRetentionMoved marks the org for the next engine retention sync when
// the retention an external identity check's session was created with is no
// longer what the settings give it: they changed between the read it was
// created from and its attach, so a sync that ran in between did not see it.
// A sync that runs after the attach sees it, so between the two none is
// missed. Its own failure is logged, not returned: the session is attached
// and the request stands.
func (s *Service) markIfRetentionMoved(ctx context.Context, orgID uuid.UUID, kind FlowKind, override, created time.Duration) {
	if kind != FlowIdentity {
		return
	}
	settings, err := s.orgSettings.Get(ctx, orgID)
	if err == nil && engineRetention(&settings, kind, override) != created {
		err = s.orgSettings.MarkEngineRetentionStale(ctx, orgID)
	}
	if err != nil {
		slog.ErrorContext(ctx, "identity proofing: engine retention of a new session not checked",
			slog.String("org_id", orgID.String()), slog.Any("error", err))
	}
}

// subjectRetentionDays is what an external person is told about their
// session's data on a flow: how long it is kept anywhere at most, the retention
// that applies (the flow's override, else the org's, never less than the legal
// retention of an identity check) with the engine's grace, rounded up to whole
// days.
func subjectRetentionDays(settings Settings, kind FlowKind, override time.Duration, now time.Time) int {
	retention := settings.baseRetention(override)
	if kind == FlowIdentity {
		retention = max(retention, settings.LegalRetention.lasts(now))
	}
	day := hoursPerDay * time.Hour
	return int((retention + retentionGrace + day - 1) / day)
}

// requestTenant is the tenant req's session runs under.
func requestTenant(req Request) proofingprovider.Tenant {
	return orgTenant(req.OrganizationID)
}

// Flows returns the org's active flows with the admin's selection applied:
// every flow for FlowsAll, else only the ones members may send a request on.
// A flow reads Allowed (members may use it) and Default independently.
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
	addresses, err := s.allFlowAddresses(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	kinds, err := s.allFlowKinds(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	unhosted, err := s.hostedDisabledFlows(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	settings, err := s.orgSettings.Get(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgFlow, 0, len(flows))
	for _, f := range flows {
		of := OrgFlow{
			Flow: f, Allowed: slices.Contains(sel.FlowIDs, f.ID) && flowCompletable(f), Default: f.ID == sel.DefaultFlowID,
			Diplomas: DiplomasOff, Address: AddressOff, Kind: FlowIdentity, HostedEnabled: !slices.Contains(unhosted, f.ID),
		}
		if kind, ok := kinds[f.ID]; ok {
			of.Kind = kind
		}
		of.RetentionDays = subjectRetentionDays(settings, of.Kind, time.Duration(f.RetentionOverrideSeconds)*time.Second, s.now())
		if mode, ok := diplomas[f.ID]; ok {
			of.Diplomas = mode
		}
		if mode, ok := addresses[f.ID]; ok {
			of.Address = mode
		}
		if view == FlowsAll || of.Allowed {
			out = append(out, of)
		}
	}
	return out, nil
}

// ConfigureFlows replaces the org's selection. Every flow members may use must
// be an active the engine flow a recipient can finish, and the default an
// identity flow that can be sent to an external person.
func (s *Service) ConfigureFlows(ctx context.Context, org Org, sel FlowSelection) error {
	if err := s.checkMemberFlowKinds(ctx, org.ID, sel.FlowIDs); err != nil {
		return err
	}
	sel, err := s.validSelection(ctx, org, sel)
	if err != nil {
		return err
	}
	return s.settings.SaveFlowSelection(ctx, org.ID, sel)
}

// validSelection deduplicates sel and checks it: every flow members may use an
// active the engine flow of the org a recipient can finish, and the default an
// active identity flow that can be sent to an external person, whether or not
// members may use it.
func (s *Service) validSelection(ctx context.Context, org Org, sel FlowSelection) (FlowSelection, error) {
	ids := make([]string, 0, len(sel.FlowIDs))
	for _, id := range sel.FlowIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sel.FlowIDs = ids
	flows, err := s.Flows(ctx, org, FlowsAll)
	if err != nil {
		return sel, err
	}
	for _, id := range ids {
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == id })
		if i < 0 {
			return sel, ErrFlowNotFound
		}
		if !flowCompletable(flows[i].Flow) {
			return sel, ErrFlowNotCompletable
		}
	}
	if sel.DefaultFlowID == "" {
		return sel, nil
	}
	i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == sel.DefaultFlowID })
	if i < 0 {
		return sel, ErrFlowNotFound
	}
	if !externalCompletable(flows[i].Flow) {
		return sel, ErrFlowNotCompletable
	}
	if flows[i].Kind.dataRequest() {
		return sel, fmt.Errorf("%w: the default flow must be an identity check", ErrInvalidInput)
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
	// A new flow's hosted page has the defaults until its admin sets it.
	in, err = withReasons(in, DefaultFlowHosted())
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
// A flow that must read the name and date of birth (identityRequired) keeps
// naming dg1: an empty list would leave what it releases to the engine. A data
// request flow is the wallet's, and not edited (ErrDataRequestFlow).
func (s *Service) EditFlow(ctx context.Context, org Org, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	in, err := normalizeFlow(in)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if err := s.refuseDataRequestFlow(ctx, org, id); err != nil {
		return proofingprovider.Flow{}, err
	}
	required, err := s.identityRequired(ctx, org.ID, id)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if required && !releasesIdentity(in.RequestedAttributes) {
		return proofingprovider.Flow{}, ErrFlowNoIdentity
	}
	hosted, err := s.flowHosted(ctx, org.ID, id)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	in, err = withReasons(in, hosted)
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
// requests run, and audits it. A flow that must read the name and date of
// birth (identityRequired) only goes back to a version that reads them.
func (s *Service) ActivateFlowVersion(ctx context.Context, org Org, id string, version int) (proofingprovider.Flow, error) {
	if err := s.refuseDataRequestFlow(ctx, org, id); err != nil {
		return proofingprovider.Flow{}, err
	}
	tenant := orgTenant(org.ID)
	required, err := s.identityRequired(ctx, org.ID, id)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if required {
		versions, err := s.FlowVersions(ctx, org, id)
		if err != nil {
			return proofingprovider.Flow{}, err
		}
		i := slices.IndexFunc(versions, func(f proofingprovider.Flow) bool { return f.Version == version })
		if i >= 0 && !readsIdentity(versions[i]) {
			return proofingprovider.Flow{}, ErrFlowNoIdentity
		}
	}
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
	if !sessionTTLAllowed(in.SessionTTLSeconds) {
		return in, fmt.Errorf("%w: the session lifetime must be 2, 5 or 10 minutes", ErrInvalidInput)
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

// flowSessionTTL is how long a session of f runs: the flow's own lifetime when
// it sets one, else orgTTL.
func flowSessionTTL(f proofingprovider.Flow, orgTTL time.Duration) time.Duration {
	if f.SessionTTLSeconds == 0 {
		return orgTTL
	}
	return time.Duration(f.SessionTTLSeconds) * time.Second
}

// Requester is who sends a request: a member, or the org's software calling
// the API.
type Requester struct {
	UserID uuid.UUID
	Name   string
	// Admin is an org admin, who may send an external person any flow.
	Admin bool
	// APIKeyID is set, and UserID nil, for the org's software calling the API.
	APIKeyID *uuid.UUID
}

// mayUseAnyFlow reports a sender who is not held to the flows members may use.
func (r Requester) mayUseAnyFlow() bool { return r.Admin || r.APIKeyID != nil }

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
// org admin made available to members, or to an external person, on any flow of
// the org an admin or an API key may send and a member only if members may use
// it; a data request from an admin or an API key runs on the org's standard
// flow of its kind (requestFlowID). It creates the request's engine session
// for the chosen app, stores the request with it, and, for a mailed request,
// mails the subject the session's vcmrtd deep link: SessionTTL runs from the
// send. An on-screen request is created only once the subject accepted what is
// collected, so its session starts then. A session whose request then fails to
// store is erased again (discardSession). Nothing is sent while the org's
// proofing is paused.
func (s *Service) CreateRequest(ctx context.Context, org Org, by Requester, in NewRequest) (Sent, error) {
	if err := s.checkActive(ctx, org.ID); err != nil {
		return Sent{}, err
	}
	in, err := normalizeRequest(in)
	if err != nil {
		return Sent{}, err
	}
	subject, settings, err := s.subject(ctx, org, in)
	if err != nil {
		return Sent{}, err
	}
	ttl := SessionTTL
	if settings != nil && settings.Session.SessionTTL != 0 {
		ttl = settings.Session.SessionTTL
	}
	flowID, err := s.requestFlowID(ctx, org, by, in)
	if err != nil {
		return Sent{}, err
	}
	tenant := orgTenant(org.ID)
	flow, err := s.sendableFlow(ctx, org, tenant, flowID, by, subject)
	if err != nil {
		return Sent{}, err
	}
	ttl = flowSessionTTL(flow, ttl)
	if in.Method == proofingprovider.MethodYivi && !yiviAppAvailable(flow) {
		return Sent{}, fmt.Errorf("%w: this flow does not run in the Yivi app", ErrInvalidInput)
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
	address, err := s.flowAddress(ctx, org.ID, flow.ID)
	if err != nil {
		return Sent{}, err
	}
	kind, err := s.flowKind(ctx, org.ID, flow.ID)
	if err != nil {
		return Sent{}, err
	}
	// A data request is an external person asking for their data; it
	// collects no diplomas and no address.
	if kind.dataRequest() {
		if settings == nil {
			return Sent{}, ErrDataFlowForMember
		}
		diplomas, address = DiplomasOff, AddressOff
	}
	if diplomas.asked() && in.Channel != ChannelHosted && in.Channel != ChannelOnScreen {
		return Sent{}, ErrDiplomasNeedPage
	}
	if address.asked() && in.Channel != ChannelHosted && in.Channel != ChannelOnScreen {
		return Sent{}, ErrAddressNeedsPage
	}
	if in.Channel == ChannelHosted {
		return s.createHosted(ctx, org, by, in, subject, *settings, flow, diplomas, address, kind, photo)
	}

	id := uuid.New()
	override := time.Duration(flow.RetentionOverrideSeconds) * time.Second
	input := proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: id.String(), TTL: ttl, Method: in.Method,
		Language: string(in.Language), Retention: engineRetention(settings, kind, override), ReferencePhoto: photo,
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
		Diplomas: diplomas, Address: address, FlowKind: kind,
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
	if settings != nil {
		s.markIfRetentionMoved(ctx, org.ID, kind, override, input.Retention)
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
		mail, err := s.proofingMail(ctx, org, settings, mailSend{
			flowID: flow.ID, subject: subject, by: by, deepLink: sess.Claim.DeepLink, ttl: ttl, locale: in.Language,
		})
		if err == nil {
			err = s.mailer.SendIdentityProofingRequested(ctx, org.ID, mail)
		}
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
// redirect must be on one of the org's allowed origins.
func (s *Service) createHosted(ctx context.Context, org Org, by Requester, in NewRequest, subject Subject,
	settings Settings, flow proofingprovider.Flow, diplomas DiplomaMode, address AddressMode, kind FlowKind, photo *proofingprovider.Image,
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
		if err := checkRedirect(in.RedirectURL, settings); err != nil {
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
		Diplomas: diplomas, Address: address, FlowKind: kind, ReferencePhoto: photo,
	})
	if err != nil {
		return Sent{}, err
	}
	return Sent{Request: req, HostedURL: s.hostedBaseURL + token}, nil
}

// normalizeRequest fills in the default app and delivery and refuses a
// combination that cannot run: the Yivi app's session starts from a QR code
// shown in the browser, and a mailed subject has no such page.
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
	case in.Channel == ChannelHosted && !in.external():
		return NewRequest{}, fmt.Errorf("%w: a link goes to an external person", ErrInvalidInput)
	case in.FlowKind != "" && !in.FlowKind.valid():
		return NewRequest{}, fmt.Errorf("%w: flowKind is identity, data_access or data_erasure", ErrInvalidInput)
	case in.FlowKind.dataRequest() && !in.external():
		return NewRequest{}, ErrDataFlowForMember
	case in.RedirectURL != "" && in.Channel != ChannelHosted:
		return NewRequest{}, fmt.Errorf("%w: only a hosted session redirects its subject", ErrInvalidInput)
	}
	return in, nil
}

// subject resolves who a new request goes to: the member, or an external
// person by the address and optional name the sender gave, with the org's
// settings. An on-screen request's subject is in front of the sender and needs
// no address.
func (s *Service) subject(ctx context.Context, org Org, in NewRequest) (Subject, *Settings, error) {
	if !in.external() {
		if strings.TrimSpace(in.SubjectBirthDate) != "" {
			return Subject{}, nil, fmt.Errorf("%w: only an external person's request can be for one known person", ErrInvalidInput)
		}
		m, err := s.requests.Member(ctx, org.ID, in.SubjectUserID)
		if err != nil {
			return Subject{}, nil, err
		}
		return Subject{Kind: SubjectMember, UserID: &m.UserID, Name: m.Name, Email: m.Email}, nil, nil
	}
	settings, err := s.orgSettings.Get(ctx, org.ID)
	if err != nil {
		return Subject{}, nil, err
	}
	name := strings.TrimSpace(in.SubjectName)
	if len(name) > maxSubjectNameLength {
		return Subject{}, nil, fmt.Errorf("%w: the name is too long", ErrInvalidInput)
	}
	birthDate, err := expectedBirthDate(in.SubjectBirthDate, name, s.now())
	if err != nil {
		return Subject{}, nil, err
	}
	subject := Subject{Kind: SubjectExternal, Name: name, BirthDate: birthDate}
	// No address is needed when nothing is mailed: on screen, a hosted link,
	// or a session the caller shows its own QR for (SkipMail).
	if (in.Channel != ChannelEmail || in.SkipMail) && strings.TrimSpace(in.SubjectEmail) == "" {
		return subject, &settings, nil
	}
	email, err := user.ParseEmail(in.SubjectEmail)
	if err != nil {
		return Subject{}, nil, fmt.Errorf("%w: enter a valid e-mail address", ErrInvalidInput)
	}
	subject.Email = string(email)
	return subject, &settings, nil
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

// identityRequired reports whether flow flowID must read the name and date of
// birth: a required diploma's holder is checked against them. (A data
// request flow must too, but it is the wallet's and never edited.)
func (s *Service) identityRequired(ctx context.Context, orgID uuid.UUID, flowID string) (bool, error) {
	mode, err := s.flowDiplomas(ctx, orgID, flowID)
	if err != nil {
		return false, err
	}
	return mode.asked(), nil
}

// readsIdentity reports whether flow f's result carries the holder's name and
// date of birth (the document data, dg1): what a request for one known person
// is matched against. The engine hands a flow back listing what it releases.
func readsIdentity(f proofingprovider.Flow) bool {
	return releasesIdentity(f.RequestedAttributes)
}

// releasesIdentity reports whether attrs release the name and date of birth:
// dg1 without the age check, which withholds the date.
func releasesIdentity(attrs []string) bool {
	return slices.Contains(attrs, attributeDocument) && !slices.Contains(attrs, attributeAgeOver)
}

// mailSend is what one proofing mail is about: the request's flow, its
// subject and sender, the session's deep link and how long it is valid, and
// the language the mail is in.
type mailSend struct {
	flowID   string
	subject  Subject
	by       Requester
	deepLink string
	ttl      time.Duration
	locale   email.Locale
}

// proofingMail is the mail a request sends. A member's is the org's; an
// external person's is signed with and styled as the flow's branding over the
// org's, with its support contact and privacy statement. A logo that cannot be
// read is left out (the wordmark shows): a cosmetic loss must not block the
// send. A flow's branding that cannot be read blocks it, since the mail would
// otherwise go out in another look than the admin set.
func (s *Service) proofingMail(ctx context.Context, org Org, settings *Settings, send mailSend) (email.ProofingMail, error) {
	flowID := send.flowID
	m := email.ProofingMail{
		To: send.subject.Email, OrgName: org.Name, RequesterName: send.by.Name, DeepLink: send.deepLink,
		ValidFor: send.ttl, Locale: send.locale,
	}
	if settings == nil {
		return m, nil
	}
	own, err := s.flowBranding(ctx, org.ID, flowID)
	if err != nil {
		return email.ProofingMail{}, err
	}
	branded := settings.brandedFor(own)
	m.OrgName, m.RequesterName = branded.SignedAs(), branded.SignedAs()
	m.SupportContact, m.PrivacyURL = branded.Branding.SupportContact, branded.Branding.PrivacyURL
	m.Brand = &email.ProofingBrand{PrimaryColor: branded.Branding.PrimaryColor}
	if branded.Branding.HasLogo {
		logo, err := s.brandLogo(ctx, org.ID, flowID, own)
		if err != nil {
			slog.WarnContext(ctx, "identity proofing: logo not read, mailing the wordmark",
				slog.String("org_id", org.ID.String()), slog.Any("error", err))
		} else {
			m.Brand.Logo = email.Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}
		}
	}
	return m, nil
}

// sendableFlow is the flow a new request runs, if the sender may use it: for a
// member, a flow the admin made available to members; for an external person,
// any flow a recipient can finish when the sender is an admin or an API key,
// else one members may use, the org's default when flowID is empty.
func (s *Service) sendableFlow(ctx context.Context, org Org, tenant proofingprovider.Tenant, flowID string,
	by Requester, subject Subject,
) (proofingprovider.Flow, error) {
	if subject.Kind == SubjectMember {
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
		// A caller that names no flow gets the org's default.
		sel, err := s.settings.FlowSelection(ctx, org.ID)
		if err != nil {
			return proofingprovider.Flow{}, err
		}
		if flowID = sel.DefaultFlowID; flowID == "" {
			return proofingprovider.Flow{}, ErrNoDefaultFlow
		}
	}
	flows, err := s.orgFlows(ctx, org, tenant, FlowsAll)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == flowID })
	if i < 0 {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	if !externalCompletable(flows[i].Flow) {
		return proofingprovider.Flow{}, ErrFlowNotCompletable
	}
	if !by.mayUseAnyFlow() && !flows[i].Allowed {
		return proofingprovider.Flow{}, ErrFlowNotAllowed
	}
	return flows[i].Flow, nil
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
	// Opening a session gives it more time in the engine than it was created
	// with: the stored cap follows, or the request would read expired while
	// its subject is still at it, and then flip to their outcome.
	if res.ExpiresAt.After(sess.ExpiresAt) {
		if err := s.requests.ExtendSession(ctx, req, sess.ID, res.ExpiresAt); err != nil {
			slog.WarnContext(ctx, "identity proofing: extend session failed",
				slog.String("request_id", req.ID.String()), slog.Any("error", err))
			return req, err
		}
		extended := *sess
		extended.ExpiresAt = res.ExpiresAt
		req.session, sess = &extended, &extended
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
		// A proven person's data request goes to review with the org's
		// sessions of that person, found once.
		if next == StatusApproved || next == StatusNeedsReview {
			if req.Status != StatusNeedsReview && s.dataRequests != nil {
				// One reconcile matches at a time, across replicas: the hosted
				// page's status polls would otherwise each search while the
				// first is still at it.
				release, ok, err := s.dataRequests.TryLockMatching(ctx, req.ID)
				if err != nil {
					slog.WarnContext(ctx, "identity proofing: lock data request matching failed",
						slog.String("request_id", req.ID.String()), slog.Any("error", err))
					return req, err
				}
				if !ok {
					return req, nil
				}
				defer release()
				// Another reconcile may have finished between this one's read
				// and the lock.
				fresh, err := s.requests.Get(ctx, req.OrganizationID, req.ID)
				if err != nil {
					slog.WarnContext(ctx, "identity proofing: read the data request to match failed",
						slog.String("request_id", req.ID.String()), slog.Any("error", err))
					return req, err
				}
				if fresh.Status != req.Status {
					return fresh, nil
				}
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
	if err := s.recordIdentityKey(ctx, tenant, req, sess, next, res); err != nil {
		slog.WarnContext(ctx, "identity proofing: record the identity match key failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return req, err
	}
	// The name read off the document is kept only for an external person the
	// sender may know only by address, and only once the document is approved
	// (or, for a data request, the person is proven): only then is the full
	// result, with its personal data, read at all.
	if req.External() && (next == StatusApproved || (req.FlowKind.dataRequest() && next == StatusNeedsReview)) {
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
	if decided.Status != StatusNeedsReview {
		return decided, nil
	}
	// The legal retention declines an erasure that holds every session found;
	// the org's erasure handling decides the rest.
	declined, err := s.declineHeldErasure(ctx, decided)
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: decline the erasure the legal retention holds failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return decided, err
	}
	if declined.Status != StatusNeedsReview {
		return declined, nil
	}
	handled, err := s.handleErasure(ctx, declined)
	if err != nil {
		// Left in review: a reviewer decides it as any other.
		slog.WarnContext(ctx, "identity proofing: decide an erasure as the org set failed",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		return declined, nil
	}
	return handled, nil
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
// rejected with errorIdentityMismatch. The chip's DG11 name counts as well as
// the MRZ's, which cuts a long name off at its field's width. A request
// already decided holds no birth date any more, and is never matched again.
func (s *Service) matchSubject(ctx context.Context, tenant proofingprovider.Tenant, req Request, res proofingprovider.Result) (Status, proofingprovider.Result, error) {
	if req.expectedBirthDate == "" {
		return StatusApproved, res, nil
	}
	identity, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil {
		return "", res, fmt.Errorf("proofing: identity to match request %s: %w", req.ID, err)
	}
	if samePerson(identity.GivenName, identity.FamilyName, identity.BirthDate, req.SubjectName, req.expectedBirthDate) ||
		samePerson(identity.FullGivenName, identity.FullFamilyName, identity.BirthDate, req.SubjectName, req.expectedBirthDate) {
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

// ExternalRequest is one of the org's external requests; see readReconciled.
func (s *Service) ExternalRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	req, err := s.requests.GetExternal(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	return s.readReconciled(ctx, req)
}

// StoredExternalRequest is one of the org's external requests as stored,
// without re-checking the engine: what a headless poll answers from.
func (s *Service) StoredExternalRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	return s.requests.GetExternal(ctx, orgID, id)
}

// ExternalRequestPage is a page of the org's external requests, newest first,
// from stored state (no the engine call), and the cursor of the next page ("" at
// the end). A cursor that does not decode is ErrInvalidInput.
func (s *Service) ExternalRequestPage(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
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
	reqs, err := s.requests.ListExternalPage(ctx, orgID, after, limit+1)
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

// CancelRequest ends an external request that has no outcome yet, in the engine too,
// and marks it cancelled. One that has an outcome or ended is ErrSessionOver.
func (s *Service) CancelRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	req, err := s.ExternalRequest(ctx, orgID, id)
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
	return s.requests.GetExternal(ctx, orgID, id)
}

// cancelOnce ends req's live engine session and marks req cancelled.
func (s *Service) cancelOnce(ctx context.Context, req Request) error {
	if st := req.EffectiveStatus(s.now()); st != StatusPending && st != StatusInProgress || req.PurgedAt != nil {
		return ErrSessionOver
	}
	var endSession func(context.Context) error
	if sess := req.liveSession(s.now()); sess != nil {
		tenant := requestTenant(req)
		endSession = func(ctx context.Context) error {
			if err := s.ips.CancelSession(ctx, tenant, sess.ID, sess.Token); err != nil {
				var rejected *proofingprovider.RejectedError
				if errors.As(err, &rejected) {
					// The engine decided it first; the notified outcome records that.
					return ErrSessionOver
				}
				return fmt.Errorf("proofing: cancel request %s: %w", req.ID, err)
			}
			return nil
		}
	}
	// Its own context: once the engine has cancelled, the row must say so even
	// if the caller has gone.
	cancelled, err := s.requests.Cancel(context.WithoutCancel(ctx), req, endSession)
	if err != nil {
		return err
	}
	if !cancelled {
		return ErrSessionOver
	}
	return nil
}

// RequestResult reads a settled external request's result from the engine, the
// wallet storing no identity, and audits identity_proofing.result_read. One
// not settled yet is ErrResultNotReady; one erased or gone in the engine is not found.
func (s *Service) RequestResult(ctx context.Context, orgID, id uuid.UUID) (Request, proofingprovider.Identity, error) {
	req, err := s.ExternalRequest(ctx, orgID, id)
	if err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	return s.requestResult(ctx, req)
}

// AdminRequestResult is RequestResult for an org admin reading one of the org's
// external requests in the wallet: audited identity_proofing.result_read with
// the admin as actor. A member's request is not found: its subject is a
// colleague, whose identity the wallet shows nowhere.
func (s *Service) AdminRequestResult(ctx context.Context, orgID, id uuid.UUID) (Request, proofingprovider.Identity, error) {
	req, err := s.Request(ctx, orgID, id, nil)
	if err != nil {
		return Request{}, proofingprovider.Identity{}, err
	}
	if !req.External() {
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

// PurgeRequest erases an external request in the engine and every personal detail
// the wallet holds of it, whatever its state; the row stays, marked purged. An
// approved identity check the org's legal retention still holds is
// ErrLegalRetention: the org may not erase it before the law lets it, as no
// "delete my data" request may.
func (s *Service) PurgeRequest(ctx context.Context, orgID, id uuid.UUID) error {
	req, err := s.requests.GetExternal(ctx, orgID, id)
	if err != nil {
		return err
	}
	if req.PurgedAt != nil {
		return nil
	}
	until, err := s.requests.HeldUntil(ctx, orgID, id)
	if err != nil {
		return err
	}
	if until != nil && s.now().Before(*until) {
		return ErrLegalRetention
	}
	return s.purge(ctx, req)
}

// purgeBatch is how many overdue requests PurgeDue takes at a time.
const purgeBatch = 100

// PurgeDue purges every request past its retention (setPurgeAt), for the
// pruner. One that fails to purge is deferred (DeferPurge) and retried by a
// later run. It first rejects the reviews left open in a paused org
// (rejectPausedReviews) and gives the engine the retention an org's settings
// say where a sync is outstanding (syncStaleEngineRetention), whose failures
// are reported once the purge ran.
func (s *Service) PurgeDue(ctx context.Context) (int64, error) {
	sweepErr := s.rejectPausedReviews(ctx)
	syncErr := s.syncStaleEngineRetention(ctx)
	purged, err := s.purgeDue(ctx)
	return purged, errors.Join(err, sweepErr, syncErr)
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
		for _, req := range due {
			err := s.lapseReview(ctx, req)
			if err != nil {
				slog.WarnContext(ctx, "identity proofing: lapse review failed",
					slog.String("request_id", req.ID.String()), slog.Any("error", err))
			} else if err = s.purge(ctx, req); err != nil {
				slog.WarnContext(ctx, "identity proofing: purge failed",
					slog.String("request_id", req.ID.String()), slog.Any("error", err))
			}
			if err != nil {
				// Deferred, it no longer holds up the requests due after it.
				if err := s.requests.DeferPurge(ctx, req.ID); err != nil {
					return purged, err
				}
				continue
			}
			purged++
		}
		if len(due) < purgeBatch {
			return purged, nil
		}
	}
}

// errorReviewLapsed is the error code of a request nobody reviewed in time: a
// data request dataRequestReviewDays after it went to review, an identity
// request its retention after (purge_at, refreshPurgeAt).
const errorReviewLapsed = "REVIEW_LAPSED"

// lapseReview rejects a request whose review is due for purge: nobody decided
// it in time, and it is not left purged in review, so the person hears the
// end. Any other request is left as it is.
func (s *Service) lapseReview(ctx context.Context, req Request) error {
	if req.Status != StatusNeedsReview || req.session == nil {
		return nil
	}
	return s.requests.RecordOutcome(ctx, req, req.session.ID, StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorReviewLapsed})
}

// purge erases req's session in the engine and what the wallet holds of it,
// together (RequestStore.Purge). A session attached since req was read (a
// hosted start racing the purge) is read back and erased too.
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
	var endSession func(context.Context) error
	if sess := req.session; sess != nil {
		endSession = func(ctx context.Context) error {
			if err := s.ips.DeleteSession(ctx, requestTenant(req), sess.ID, sess.Token); err != nil {
				return fmt.Errorf("proofing: purge request %s: %w", req.ID, err)
			}
			return nil
		}
	}
	return s.requests.Purge(ctx, req, endSession)
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
	claims, err := s.yiviClaims(ctx, req)
	if err != nil {
		return YiviStart{}, err
	}
	started, err := s.verifier.StartPresentation(ctx, openid4vpverifier.ScopeProofing, claims...)
	if err != nil {
		return YiviStart{}, err
	}
	if err := s.requests.SetYiviTransaction(ctx, req, sess.ID, started.TransactionID); err != nil {
		return YiviStart{}, err
	}
	return YiviStart{WalletLink: started.WalletLink, ExpiresAt: sess.ExpiresAt}, nil
}

// yiviClaims are the claims req's Yivi disclosure asks for besides the
// identity: the photo when the flow keeps it, and an age check's age claim in
// place of the date of birth. The flow is the version req's session pinned,
// else the active one.
func (s *Service) yiviClaims(ctx context.Context, req Request) ([]string, error) {
	versions, err := s.ips.ListFlowVersions(ctx, requestTenant(req), req.FlowID)
	if err != nil {
		return nil, fmt.Errorf("proofing: flow of request %s: %w", req.ID, err)
	}

	i := slices.IndexFunc(versions, func(f proofingprovider.Flow) bool { return f.Version == req.FlowVersion })
	if i < 0 {
		i = slices.IndexFunc(versions, func(f proofingprovider.Flow) bool { return f.Active })
	}
	if i < 0 {
		return nil, ErrFlowNotFound
	}

	f := versions[i]
	var claims []string
	if keepsFacePhoto(f) {
		claims = append(claims, openid4vpverifier.ClaimPhoto)
	}

	if !slices.Contains(f.RequestedAttributes, attributeAgeOver) {
		return claims, nil
	}
	claim, ok := openid4vpverifier.AgeClaim(f.AgeOver)
	if !ok {
		return nil, fmt.Errorf("%w: the Yivi app cannot answer age %d", ErrInvalidInput, f.AgeOver)
	}
	return append(claims, claim), nil
}

// YiviDisclosure hands the subject's finished disclosure to the engine, which
// decides the session with it, or answers ErrDisclosurePending while the
// subject has not finished in the Yivi app. The decision is recorded at once.
// The photo and claims pass through and are not kept.
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
		return proofingprovider.YiviDisclosure{}, errors.New("proofing: the presentation carries no passport, id-card or residence permit")
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
	if requestedBy != nil {
		if err := memberTimeline(page.Events, *requestedBy); err != nil {
			return nil, err
		}
	}
	return page.Events, nil
}

// adminActorLabel names whoever acted on a member's request when that is
// not the member: an admin reading the identity or deciding its review.
const adminActorLabel = "admin"

// memberTimeline trims a request's events to what the member who sent it may
// read: what happened and when, as the org audit log shows an ordinary member,
// plus their own name. The subject's personal fields and a reviewer's reason go
// (the identity result is admin-only), and another actor becomes "an admin".
func memberTimeline(events []audit.Event, member uuid.UUID) error {
	for i := range events {
		e := &events[i]
		if e.Actor != nil && e.Actor.UserID != member {
			e.Actor = nil
			label := adminActorLabel
			e.ActorLabel = &label
		}
		if len(e.Metadata) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(e.Metadata, &fields); err != nil {
			return fmt.Errorf("proofing: read event %s metadata: %w", e.ID, err)
		}
		for _, key := range audit.IdentityProofingPersonalKeys {
			delete(fields, key)
		}
		trimmed, err := json.Marshal(fields)
		if err != nil {
			return fmt.Errorf("proofing: trim event %s metadata: %w", e.ID, err)
		}
		e.Metadata = trimmed
	}
	return nil
}

// CreateAPIKey adds a key with every scope to the org; the returned secret is
// shown once.
func (s *Service) CreateAPIKey(ctx context.Context, orgID, createdBy uuid.UUID, name string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxAPIKeyNameLength {
		return APIKey{}, "", fmt.Errorf("%w: an API key needs a name of at most %d characters", ErrInvalidInput, maxAPIKeyNameLength)
	}
	return s.apiKeys.Create(ctx, orgID, createdBy, name, slices.Clone(APIKeyScopes))
}

// APIKeys lists the org's keys, revoked ones included.
func (s *Service) APIKeys(ctx context.Context, orgID uuid.UUID) ([]APIKey, error) {
	return s.apiKeys.List(ctx, orgID)
}

// RevokeAPIKey stops one of the org's keys authenticating.
func (s *Service) RevokeAPIKey(ctx context.Context, orgID, id uuid.UUID) (APIKey, error) {
	return s.apiKeys.Revoke(ctx, orgID, id)
}

// AuthenticateAPIKey resolves a raw key to who it acts for.
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string) (APIKeyCaller, error) {
	return s.apiKeys.Authenticate(ctx, raw)
}

// TouchAPIKey records caller's key as used.
func (s *Service) TouchAPIKey(ctx context.Context, caller APIKeyCaller) error {
	return s.apiKeys.Touch(ctx, caller)
}

// Webhook returns the org's endpoint, or ErrWebhookNotFound.
func (s *Service) Webhook(ctx context.Context, orgID uuid.UUID) (Webhook, error) {
	return s.webhooks.Get(ctx, orgID)
}

// SaveWebhook sets the org's endpoint: an absolute https URL (checked again
// at every send, against the address it resolves to) and at least one known
// event. A new endpoint's signing secret is returned, the one time it is
// readable.
func (s *Service) SaveWebhook(ctx context.Context, orgID uuid.UUID, rawURL string, events []string) (Webhook, string, error) {
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
	return s.webhooks.Save(ctx, orgID, rawURL, picked)
}

// RotateWebhookSecret replaces the org's signing secret and returns it once.
func (s *Service) RotateWebhookSecret(ctx context.Context, orgID uuid.UUID) (Webhook, string, error) {
	return s.webhooks.RotateSecret(ctx, orgID)
}

// RemoveWebhook deletes the org's endpoint and its waiting deliveries.
func (s *Service) RemoveWebhook(ctx context.Context, orgID uuid.UUID) error {
	return s.webhooks.Remove(ctx, orgID)
}

// SendTestWebhook queues a test event for the org's endpoint.
func (s *Service) SendTestWebhook(ctx context.Context, orgID uuid.UUID) error {
	return s.webhooks.SendTest(ctx, orgID)
}

// WebhookDeliveries lists the org's most recent deliveries.
func (s *Service) WebhookDeliveries(ctx context.Context, orgID uuid.UUID) ([]Delivery, error) {
	return s.webhooks.Deliveries(ctx, orgID)
}

// WebhookHealth reports how the org's endpoint answers.
func (s *Service) WebhookHealth(ctx context.Context, orgID uuid.UUID) (WebhookHealth, error) {
	return s.webhooks.Health(ctx, orgID)
}

// Settings returns what the org set once for all its flows: the defaults when
// it set none.
func (s *Service) Settings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	return s.orgSettings.Get(ctx, orgID)
}

// SaveBranding replaces the org's branding. The colour is a #rrggbb hex, the
// privacy statement an https URL; empty fields fall back.
func (s *Service) SaveBranding(ctx context.Context, orgID uuid.UUID, b Branding, logo LogoChange) (Settings, error) {
	b, err := checkBranding(b)
	if err != nil {
		return Settings{}, err
	}
	return s.orgSettings.SaveBranding(ctx, orgID, b, logo)
}

// checkBranding trims b's fields and refuses one that cannot be saved.
func checkBranding(b Branding) (Branding, error) {
	b.DisplayName = strings.TrimSpace(b.DisplayName)
	b.PrimaryColor = strings.TrimSpace(b.PrimaryColor)
	b.SecondaryColor = strings.TrimSpace(b.SecondaryColor)
	b.FontFamily = strings.TrimSpace(b.FontFamily)
	for name, color := range map[string]*string{
		"text": &b.TextColor, "surface": &b.SurfaceColor, "border": &b.BorderColor, "link": &b.LinkColor,
		"success": &b.SuccessColor, "warning": &b.WarningColor, "error": &b.ErrorColor, "top bar": &b.TopbarColor,
	} {
		*color = strings.TrimSpace(*color)
		if *color != "" && !hexColor.MatchString(*color) {
			return Branding{}, fmt.Errorf("%w: the %s colour must be a hex colour like #1f5b4a", ErrInvalidInput, name)
		}
	}
	if b.FontFamily != "" && !themesettings.ValidFontFamily(b.FontFamily) {
		return Branding{}, fmt.Errorf("%w: the font must be a comma-separated font-family list", ErrInvalidInput)
	}
	b.SupportContact = strings.TrimSpace(b.SupportContact)
	b.PrivacyURL = strings.TrimSpace(b.PrivacyURL)
	switch {
	case len(b.DisplayName) > maxDisplayNameLength:
		return Branding{}, fmt.Errorf("%w: the display name is too long", ErrInvalidInput)
	case b.PrimaryColor != "" && !hexColor.MatchString(b.PrimaryColor):
		return Branding{}, fmt.Errorf("%w: the primary colour must be a hex colour like #1f5b4a", ErrInvalidInput)
	case b.SecondaryColor != "" && !hexColor.MatchString(b.SecondaryColor):
		return Branding{}, fmt.Errorf("%w: the secondary colour must be a hex colour like #1f5b4a", ErrInvalidInput)
	case len(b.SupportContact) > maxSupportContactLen:
		return Branding{}, fmt.Errorf("%w: the support contact is too long", ErrInvalidInput)
	case len(b.PrivacyURL) > maxWebhookURLLength:
		return Branding{}, fmt.Errorf("%w: the privacy statement URL is too long", ErrInvalidInput)
	}
	if b.PrivacyURL != "" {
		if _, err := (safehttp.Policy{}).CheckURL(b.PrivacyURL); err != nil {
			return Branding{}, fmt.Errorf("%w: the privacy statement must be an absolute https URL", ErrInvalidInput)
		}
	}
	return b, nil
}

// Logo returns the org's logo, or ErrNoLogo.
func (s *Service) Logo(ctx context.Context, orgID uuid.UUID) (Logo, error) {
	return s.orgSettings.Logo(ctx, orgID)
}

// sessionTTLAllowed is whether a flow may set seconds as its session lifetime:
// 0 for none, else one of SessionTTLOptions. Compared in seconds, so a huge
// value cannot wrap to an option.
func sessionTTLAllowed(seconds int) bool {
	return seconds == 0 || slices.ContainsFunc(SessionTTLOptions, func(option time.Duration) bool {
		return int(option/time.Second) == seconds
	})
}

// checkSettingsChange reports whether every session setting in change is one
// the org may change. Its session lifetime and data retention are fixed
// defaults (SessionTTL, defaultDataRetentionDays): a flow sets its own.
func checkSettingsChange(change SessionSettingsChange) error {
	if change.SessionTTL != nil || change.DataRetentionDays != nil {
		return fmt.Errorf("%w: the session lifetime and the data retention are set per flow", ErrInvalidInput)
	}
	if change.ErasureHandling != nil && !change.ErasureHandling.valid() {
		return fmt.Errorf("%w: erasure requests are decided by review or automatically", ErrInvalidInput)
	}
	return nil
}

// SettingsChange is a partial update of the org's session settings, legal
// retention and redirect origins; a nil field is left as it is. A pointer to
// the zero LegalRetention removes the legal retention.
type SettingsChange struct {
	SessionTTL        *time.Duration
	DataRetentionDays *int
	ErasureHandling   *ErasureHandling
	LegalRetention    *LegalRetention
	RedirectOrigins   *[]string
}

func (c SettingsChange) session() SessionSettingsChange {
	return SessionSettingsChange{
		SessionTTL: c.SessionTTL, DataRetentionDays: c.DataRetentionDays, ErasureHandling: c.ErasureHandling,
	}
}

// UpdateSettings applies change once every field in it is valid, in one
// transaction, so a rejected or failed update leaves the settings, and the
// audit log, as they were.
func (s *Service) UpdateSettings(ctx context.Context, orgID uuid.UUID, change SettingsChange) (Settings, error) {
	session := change.session()
	if err := checkSettingsChange(session); err != nil {
		return Settings{}, err
	}
	var legal *LegalRetention
	if change.LegalRetention != nil {
		tidy := change.LegalRetention.tidy()
		if err := tidy.check(); err != nil {
			return Settings{}, err
		}
		legal = &tidy
	}
	var origins *[]string
	if change.RedirectOrigins != nil {
		normalized, err := normalizeOrigins(*change.RedirectOrigins)
		if err != nil {
			return Settings{}, err
		}
		origins = &normalized
	}
	if session.empty() && origins == nil && legal == nil {
		return s.orgSettings.Get(ctx, orgID)
	}
	current, err := s.orgSettings.Save(ctx, orgID, session, legal, origins)
	if err != nil {
		return Settings{}, err
	}
	// Last, and not the update's failure: the change is saved, and the org
	// stays marked until a sync succeeds, which the pruner retries.
	if legal != nil {
		if err := s.syncEngineRetention(ctx, orgID); err != nil {
			slog.ErrorContext(ctx, "identity proofing: engine retention sync failed; the pruner retries it",
				slog.String("org_id", orgID.String()), slog.Any("error", err))
		}
	}
	return current, nil
}

// Stats counts the external requests sent within statsWindow, per flow,
// narrowed to one member's when requestedBy is set.
func (s *Service) Stats(ctx context.Context, orgID uuid.UUID, requestedBy *uuid.UUID) ([]StatsRow, time.Time, error) {
	since := s.now().Add(-statsWindow)
	rows, err := s.requests.Stats(ctx, orgID, requestedBy, since)
	return rows, since, err
}
