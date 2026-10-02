package proofing

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

type fakeSettings struct {
	selection  FlowSelection
	flowEvents []string
}

func (f *fakeSettings) FlowSelection(context.Context, uuid.UUID) (FlowSelection, error) {
	return f.selection, nil
}

func (f *fakeSettings) SaveFlowSelection(_ context.Context, _ uuid.UUID, sel FlowSelection) error {
	f.selection = sel
	return nil
}

func (f *fakeSettings) RecordFlowEvent(_ context.Context, _ uuid.UUID, action string, _ proofingprovider.Flow) error {
	f.flowEvents = append(f.flowEvents, action)
	return nil
}

type fakeRequests struct {
	members  map[uuid.UUID]Member
	linkHash []byte
	reviews  []string
	// stored is the last request created.
	stored      *Request
	resultReads int
	// page is what ListPage answers from; pages records each cursor asked for.
	page     []Request
	pages    []*RequestCursor
	created  []NewStoredRequest
	attached []proofingprovider.Session
	outcomes []Status
	results  []proofingprovider.Result
	names    []string
	started  int
	ended    int
	methods  []proofingprovider.Method
	// handovers counts the new codes recorded.
	handovers int
	// actors is the audit actor label of each start and outcome recorded, ""
	// for the system.
	actors []string
}

// actorLabel is the audit actor label ctx carries, "" for none.
func actorLabel(ctx context.Context) string {
	a, _ := audit.ActorFromContext(ctx)
	return a.Label
}

func (f *fakeRequests) Create(_ context.Context, in NewStoredRequest) (Request, error) {
	f.created = append(f.created, in)
	req := Request{
		ID: in.ID, OrganizationID: in.OrgID, SubjectUserID: in.Subject.UserID, CustomerID: in.Subject.CustomerID,
		SubjectName:  in.Subject.Name,
		SubjectEmail: in.Subject.Email, FlowID: in.Flow.ID, FlowName: in.Flow.Name, FlowVersion: in.Flow.Version,
		Status: StatusPending, LinkExpiresAt: in.LinkExpiresAt, Method: in.Method, Mode: in.Mode,
		Hosted:                 in.LinkTokenHash != nil,
		RequiredAssuranceLevel: in.Flow.RequiredAssuranceLevel,
		RedirectURL:            in.RedirectURL, Language: in.Language, Diplomas: in.Diplomas,
		ExpectsSubject: in.Subject.BirthDate != "", expectedBirthDate: in.Subject.BirthDate,
		FlowKind: in.FlowKind,
	}
	f.stored = &req
	f.linkHash = in.LinkTokenHash
	return req, nil
}

func (f *fakeRequests) AttachSession(_ context.Context, req Request, sess proofingprovider.Session) (bool, error) {
	if f.stored.session != nil {
		return false, nil
	}
	f.attached = append(f.attached, sess)
	f.stored.session = &ipsSession{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt}
	f.stored.FlowVersion = sess.FlowVersion
	if req.Method != "" {
		f.stored.Method = req.Method
	}
	return true, nil
}

func (f *fakeRequests) ReferencePhoto(context.Context, Request) (*proofingprovider.Image, error) {
	if len(f.created) == 0 {
		return nil, nil
	}
	return f.created[len(f.created)-1].ReferencePhoto, nil
}

func (f *fakeRequests) LapseLinks(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

func (f *fakeRequests) RecordReviewDecision(_ context.Context, _ Request, decided Status, reason, _ string) error {
	f.reviews = append(f.reviews, string(decided)+": "+reason)
	return nil
}

func (f *fakeRequests) GetByLinkToken(_ context.Context, hash []byte) (Request, error) {
	if f.stored == nil || f.linkHash == nil || string(f.linkHash) != string(hash) {
		return Request{}, ErrRequestNotFound
	}
	return *f.stored, nil
}

func (f *fakeRequests) List(context.Context, uuid.UUID, RequestFilter) ([]Request, error) {
	if f.stored == nil {
		return []Request{}, nil
	}
	return []Request{*f.stored}, nil
}

func (f *fakeRequests) MarkStarted(ctx context.Context, _ Request, _ string, method proofingprovider.Method) error {
	f.actors = append(f.actors, actorLabel(ctx))
	f.methods = append(f.methods, method)
	f.started++
	f.stored.Status = StatusInProgress
	return nil
}

func (f *fakeRequests) RecordHandover(context.Context, Request, time.Time) error {
	f.handovers++
	return nil
}

func (f *fakeRequests) EndSession(context.Context, Request, string, proofingprovider.Status, proofingprovider.Method) error {
	f.ended++
	now := time.Now()
	f.stored.session.EndedAt = &now
	return nil
}

func (f *fakeRequests) Cancel(context.Context, Request) (bool, error) {
	if f.stored.CancelledAt != nil {
		return false, nil
	}
	now := time.Now()
	f.stored.CancelledAt = &now
	return true, nil
}

func (f *fakeRequests) ListPage(_ context.Context, _, customerID uuid.UUID, after *RequestCursor, limit int) ([]Request, error) {
	f.pages = append(f.pages, after)
	out := []Request{}
	for _, r := range f.page {
		if len(out) < limit && r.CustomerID != nil && *r.CustomerID == customerID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRequests) RecordResultRead(context.Context, Request) error {
	f.resultReads++
	return nil
}

func (f *fakeRequests) Purge(context.Context, Request) error {
	now := time.Now()
	f.stored.PurgedAt, f.stored.ProofedName = &now, ""
	f.stored.SubjectName, f.stored.SubjectEmail = "", ""
	return nil
}

func (f *fakeRequests) ListPurgeDue(context.Context, int) ([]Request, error) {
	if f.stored == nil || f.stored.PurgedAt != nil || f.stored.PurgeAt == nil || f.stored.PurgeAt.After(time.Now()) {
		return nil, nil
	}
	return []Request{*f.stored}, nil
}

func (f *fakeRequests) Member(_ context.Context, _, userID uuid.UUID) (Member, error) {
	m, ok := f.members[userID]
	if !ok {
		return Member{}, ErrMemberNotFound
	}
	return m, nil
}

func (f *fakeRequests) RecordOutcome(ctx context.Context, _ Request, _ string, status Status, res proofingprovider.Result) error {
	f.actors = append(f.actors, actorLabel(ctx))
	f.outcomes = append(f.outcomes, status)
	f.results = append(f.results, res)
	f.names = append(f.names, res.Name)
	f.stored.Status = status
	f.stored.ProofedName = res.Name
	f.stored.AssuranceLevel, f.stored.EIDASLevel, f.stored.ErrorCode = res.AssuranceLevel, res.EIDASLevel, res.ErrorCode
	if status == StatusApproved || status == StatusRejected {
		f.stored.expectedBirthDate = ""
	}
	return nil
}

func (f *fakeRequests) GetForCustomer(_ context.Context, _, customerID, id uuid.UUID) (Request, error) {
	if f.stored == nil || f.stored.ID != id || f.stored.CustomerID == nil || *f.stored.CustomerID != customerID {
		return Request{}, ErrRequestNotFound
	}
	return *f.stored, nil
}

func (f *fakeRequests) Get(_ context.Context, _, id uuid.UUID) (Request, error) {
	if f.stored == nil || f.stored.ID != id {
		return Request{}, ErrRequestNotFound
	}
	return *f.stored, nil
}

func (f *fakeRequests) ListDue(_ context.Context, now time.Time, _ int) ([]Request, error) {
	if f.stored == nil || !f.stored.stillOpen() || now.Before(f.stored.session.ExpiresAt) {
		return []Request{}, nil
	}
	return []Request{*f.stored}, nil
}

func (f *fakeRequests) NextDeadline(_ context.Context, now time.Time) (time.Time, error) {
	if f.stored == nil || !f.stored.stillOpen() || !now.Before(f.stored.session.ExpiresAt) {
		return time.Time{}, nil
	}
	return f.stored.session.ExpiresAt, nil
}

func (f *fakeRequests) GetBySession(_ context.Context, sessionID string) (Request, error) {
	if f.stored == nil || f.stored.session == nil || f.stored.session.ID != sessionID {
		return Request{}, ErrRequestNotFound
	}
	return *f.stored, nil
}

func (f *fakeRequests) SetYiviTransaction(_ context.Context, _ Request, _, transactionID string) error {
	f.stored.yiviTransactionID = transactionID
	return nil
}

func (f *fakeRequests) Stats(context.Context, uuid.UUID, *uuid.UUID, time.Time) ([]StatsRow, error) {
	return []StatsRow{}, nil
}

// fakeAPIKeys stores nothing: Create answers the key it was asked for.
type fakeAPIKeys struct{}

func (fakeAPIKeys) Create(_ context.Context, orgID, customerID, _ uuid.UUID, name string, mode Mode, scopes []string) (APIKey, string, error) {
	return APIKey{ID: uuid.New(), OrganizationID: orgID, CustomerID: customerID, Name: name, Mode: mode, Scopes: scopes}, "yp_live_x", nil
}

func (fakeAPIKeys) List(context.Context, uuid.UUID, uuid.UUID) ([]APIKey, error) { return nil, nil }

func (fakeAPIKeys) Revoke(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (APIKey, error) {
	return APIKey{}, ErrAPIKeyNotFound
}

func (fakeAPIKeys) Authenticate(context.Context, string) (APIKeyCaller, error) {
	return APIKeyCaller{}, ErrAPIKeyInvalid
}

type fakeCustomers struct {
	byID  map[uuid.UUID]Customer
	gets  int
	saved []FlowSelection
	logo  CustomerLogo
}

func (f *fakeCustomers) List(context.Context, uuid.UUID) ([]Customer, error) {
	out := []Customer{}
	for _, c := range f.byID {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeCustomers) Get(_ context.Context, _, id uuid.UUID) (Customer, error) {
	f.gets++
	c, ok := f.byID[id]
	if !ok {
		return Customer{}, ErrCustomerNotFound
	}
	return c, nil
}

func (f *fakeCustomers) Create(_ context.Context, orgID, _ uuid.UUID, name string) (Customer, error) {
	c := Customer{ID: uuid.New(), OrganizationID: orgID, Name: name, Flows: FlowSelection{FlowIDs: []string{}}}
	f.byID[c.ID] = c
	return c, nil
}

func (f *fakeCustomers) Rename(_ context.Context, _, id uuid.UUID, name string) (Customer, error) {
	c := f.byID[id]
	c.Name = name
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) SaveFlows(_ context.Context, _, id uuid.UUID, sel FlowSelection) (Customer, error) {
	f.saved = append(f.saved, sel)
	c := f.byID[id]
	c.Flows = sel
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) SetPaused(_ context.Context, _, id uuid.UUID, paused bool) (Customer, error) {
	c := f.byID[id]
	c.PausedAt = nil
	if paused {
		now := time.Now()
		c.PausedAt = &now
	}
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) SaveSettings(_ context.Context, _, id uuid.UUID, settings CustomerSettings) (Customer, error) {
	c := f.byID[id]
	c.Settings = settings
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) Remove(_ context.Context, _, id uuid.UUID) error {
	if _, ok := f.byID[id]; !ok {
		return ErrCustomerNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeCustomers) SaveBranding(_ context.Context, _, id uuid.UUID, b CustomerBranding, logo LogoChange) (Customer, error) {
	c := f.byID[id]
	if logo.Replace {
		b.HasLogo = len(logo.Logo.Bytes) > 0
		f.logo = logo.Logo
	} else {
		b.HasLogo = c.Branding.HasLogo
	}
	c.Branding = b
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) SaveRedirectOrigins(_ context.Context, _, id uuid.UUID, origins []string) (Customer, error) {
	c := f.byID[id]
	c.RedirectOrigins = origins
	f.byID[id] = c
	return c, nil
}

func (f *fakeCustomers) Logo(_ context.Context, _, id uuid.UUID) (CustomerLogo, error) {
	if !f.byID[id].Branding.HasLogo {
		return CustomerLogo{}, ErrNoCustomerLogo
	}
	return f.logo, nil
}

type fakeIPS struct {
	flows          []proofingprovider.Flow
	flowLists      int
	decisions      []proofingprovider.ReviewDecision
	sessionKeys    []proofingprovider.Tenant
	statusReads    int
	resultReads    int
	result         proofingprovider.Result
	resultErr      error
	sessions       []proofingprovider.SessionInput
	sessionExpires time.Time
	createdFlows   []proofingprovider.FlowSpec
	versions       []string
	// noClaim makes a created session come without a vcmrtd link.
	noClaim bool
	// references are the Yivi disclosures handed to IPS as face references.
	references []proofingprovider.Reference
	// handovers counts fresh claim links asked for; handoverErr fails them.
	// slotClaimed makes the link a handover from a phone that held the session.
	handovers   int
	handoverErr error
	slotClaimed bool
	// cancels and deletes count the sessions ended and erased at IPS.
	cancels   int
	cancelErr error
	deletes   int
	deleteErr error
}

func (f *fakeIPS) SessionIdentity(_ context.Context, _ proofingprovider.Tenant, _, _ string) (proofingprovider.Identity, error) {
	f.resultReads++
	return proofingprovider.Identity{
		Result: f.result, GivenName: "Anna", FamilyName: "Jansen", BirthDate: testBirthDate,
	}, f.resultErr
}

func (f *fakeIPS) CancelSession(context.Context, proofingprovider.Tenant, string, string) error {
	f.cancels++
	return f.cancelErr
}

func (f *fakeIPS) DeleteSession(context.Context, proofingprovider.Tenant, string, string) error {
	f.deletes++
	return f.deleteErr
}

func (f *fakeIPS) SessionHandover(_ context.Context, _ proofingprovider.Tenant, sessionID, _ string) (proofingprovider.Claim, error) {
	f.handovers++
	if f.handoverErr != nil {
		return proofingprovider.Claim{}, f.handoverErr
	}
	return proofingprovider.Claim{DeepLink: "vcmrtd://verify?handover=fresh-" + sessionID, ExpiresAt: time.Now().Add(time.Minute), Handover: f.slotClaimed}, nil
}

func (f *fakeIPS) ListFlows(context.Context, proofingprovider.Tenant) ([]proofingprovider.Flow, error) {
	f.flowLists++
	return f.flows, nil
}

func (f *fakeIPS) CreateFlow(_ context.Context, _ proofingprovider.Tenant, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	f.createdFlows = append(f.createdFlows, in)
	return proofingprovider.Flow{FlowSpec: in, ID: "new", Version: 1, Active: true}, nil
}

func (f *fakeIPS) CreateFlowVersion(_ context.Context, _ proofingprovider.Tenant, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	if !slices.ContainsFunc(f.flows, func(fl proofingprovider.Flow) bool { return fl.ID == id }) {
		return proofingprovider.Flow{}, proofingprovider.ErrNotFound
	}
	f.versions = append(f.versions, id)
	f.createdFlows = append(f.createdFlows, in)
	return proofingprovider.Flow{FlowSpec: in, ID: id, Version: 2, Active: true}, nil
}

func (f *fakeIPS) ListFlowVersions(_ context.Context, _ proofingprovider.Tenant, id string) ([]proofingprovider.Flow, error) {
	return []proofingprovider.Flow{{ID: id, Version: 1}, {ID: id, Version: 2, Active: true}}, nil
}

func (*fakeIPS) ActivateFlowVersion(_ context.Context, _ proofingprovider.Tenant, id string, version int) (proofingprovider.Flow, error) {
	return proofingprovider.Flow{ID: id, Version: version, Active: true}, nil
}

func (f *fakeIPS) CreateSession(_ context.Context, tenant proofingprovider.Tenant, in proofingprovider.SessionInput) (proofingprovider.Session, error) {
	f.sessions = append(f.sessions, in)
	f.sessionKeys = append(f.sessionKeys, tenant)
	id := "ses-" + in.ClientReference
	sess := proofingprovider.Session{ID: id, Token: "tok", ExpiresAt: f.sessionExpires, FlowVersion: testPinnedVersion}
	if !f.noClaim {
		sess.Claim = &proofingprovider.Claim{DeepLink: "vcmrtd://verify?handover=" + id, ExpiresAt: time.Now().Add(time.Minute)}
	}
	return sess, nil
}

func (f *fakeIPS) SessionStatus(context.Context, proofingprovider.Tenant, string, string) (proofingprovider.Result, error) {
	f.statusReads++
	res := f.result
	res.Name = ""
	return res, f.resultErr
}

func (f *fakeIPS) SessionResult(context.Context, proofingprovider.Tenant, string, string) (proofingprovider.Result, error) {
	f.resultReads++
	return f.result, f.resultErr
}

func (f *fakeIPS) SubmitReference(_ context.Context, _ proofingprovider.Tenant, _, _ string, ref proofingprovider.Reference) (proofingprovider.YiviDisclosure, error) {
	f.references = append(f.references, ref)
	return proofingprovider.YiviDisclosure{OK: true, StableFrames: 3}, nil
}

// fakeVerifier is the OpenID4VP verifier: a presentation stays pending until
// presentation is set.
type fakeVerifier struct {
	started      int
	presentation *openid4vpverifier.Presentation
	polled       []string
}

func (f *fakeVerifier) StartPresentation(_ context.Context, scope openid4vpverifier.Scope, _ ...string) (openid4vpverifier.Session, error) {
	if scope != openid4vpverifier.ScopeProofing {
		return openid4vpverifier.Session{}, errors.New("unexpected scope")
	}
	f.started++
	return openid4vpverifier.Session{TransactionID: "tx-" + strconv.Itoa(f.started), WalletLink: "openid4vp://?request_uri=x"}, nil
}

func (f *fakeVerifier) Result(_ context.Context, transactionID string) (openid4vpverifier.Presentation, error) {
	f.polled = append(f.polled, transactionID)
	if f.presentation == nil {
		return openid4vpverifier.Presentation{}, openid4vpverifier.ErrPending
	}
	return *f.presentation, nil
}

func (*fakeIPS) SubmitFaceFrame(context.Context, string, string) (proofingprovider.FaceVerdict, error) {
	return proofingprovider.FaceVerdict{}, nil
}

// DecideReview settles the session the way IPS does: the next read has the outcome.
func (f *fakeIPS) DecideReview(_ context.Context, _ proofingprovider.Tenant, _, _ string, d proofingprovider.ReviewDecision) error {
	f.decisions = append(f.decisions, d)
	if f.result.Status != proofingprovider.StatusNeedsReview {
		return &proofingprovider.RejectedError{Status: 409, Message: "not under review"}
	}
	f.result = proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: d.ErrorCode}
	if d.Approve {
		f.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
	}
	return nil
}

type sentMail struct {
	to, orgName, requester, deepLink string
	validFor                         time.Duration
	mail                             email.ProofingMail
}

type fakeMailer struct {
	sent []sentMail
	err  error
}

func (f *fakeMailer) SendIdentityProofingRequested(_ context.Context, _ uuid.UUID, m email.ProofingMail) error {
	f.sent = append(f.sent, sentMail{
		to: m.To, orgName: m.OrgName, requester: m.RequesterName, deepLink: m.DeepLink, validFor: m.ValidFor, mail: m,
	})
	return f.err
}

func testFlow(id, name string, steps []string, selfieLocation string) proofingprovider.Flow {
	return proofingprovider.Flow{
		FlowSpec: proofingprovider.FlowSpec{Name: name, Steps: steps, SelfieLocation: selfieLocation},
		ID:       id, Version: 1, Active: true,
	}
}

func withFaceProvider(f proofingprovider.Flow, provider string) proofingprovider.Flow {
	f.FaceProvider = provider
	return f
}

var (
	// appFlow's face check runs on Regula, so the Yivi app can run it too.
	appFlow  = withFaceProvider(testFlow("f-app", "Passport + face", []string{"nfc_read", "face_match"}, "native"), faceProviderRegula)
	chipFlow = testFlow("f-chip", "Passport only", []string{"document_capture", "nfc_read"}, "native")
	// browserFlow captures the face in a browser a recipient cannot reach.
	browserFlow = testFlow("f-web", "Web selfie", []string{"selfie"}, selfieLocationBrowser)

	testOrg = Org{ID: uuid.New(), Name: "Acme BV"}
	alex    = Member{UserID: uuid.New(), Name: "Alex Jansen", Email: "alex@example.org", Role: "admin", MemberType: "employee"}
	// initech is a customer with chipFlow assigned (a flow members may not use)
	// as its default.
	initech     = Customer{ID: uuid.New(), OrganizationID: testOrg.ID, Name: "Initech", Flows: FlowSelection{FlowIDs: []string{chipFlow.ID}, DefaultFlowID: chipFlow.ID}, HasLiveKey: true}
	testSession = SessionTTL
)

// testPinnedVersion is the flow version the fake IPS pins a session to, newer
// than the listed one a request is sent on.
const testPinnedVersion = 2

type fixture struct {
	svc       *Service
	settings  *fakeSettings
	requests  *fakeRequests
	customers *fakeCustomers
	ips       *fakeIPS
	verifier  *fakeVerifier
	mailer    *fakeMailer
}

// newFixture builds a service over fakes: appFlow is made available to
// members as the default; alex is a member and initech a customer.
func newFixture() fixture {
	f := fixture{
		settings: &fakeSettings{selection: FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}},
		requests: &fakeRequests{members: map[uuid.UUID]Member{alex.UserID: alex}},
		ips: &fakeIPS{
			flows: []proofingprovider.Flow{appFlow, chipFlow, browserFlow}, sessionExpires: time.Now().Add(testSession),
			result: proofingprovider.Result{Status: proofingprovider.StatusCreated},
		},
		customers: &fakeCustomers{byID: map[uuid.UUID]Customer{initech.ID: initech}},
		verifier:  &fakeVerifier{},
		mailer:    &fakeMailer{},
	}
	f.svc = NewService(Stores{Settings: f.settings, Requests: f.requests, Customers: f.customers, APIKeys: fakeAPIKeys{}},
		f.ips, f.verifier, f.mailer)
	return f
}

func (f fixture) send(t *testing.T) Sent {
	t.Helper()
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	return sent
}

// reconcile is IPS reporting a change to the sent request's session, then a
// list read of the request.
func (f fixture) reconcile(t *testing.T) Request {
	t.Helper()
	if f.requests.stored == nil || f.requests.stored.session == nil {
		t.Fatal("reconcile: no request with a session was sent")
	}
	f.svc.SessionChanged(context.Background(), f.requests.stored.session.ID)
	reqs, err := f.svc.Requests(context.Background(), testOrg.ID, RequestFilter{})
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Requests = %+v, %v; want the one request", reqs, err)
	}
	return reqs[0]
}

func TestFlowsAppliesSelection(t *testing.T) {
	f := newFixture()
	all, err := f.svc.Flows(context.Background(), testOrg, true)
	if err != nil {
		t.Fatalf("Flows(all): %v", err)
	}
	if len(all) != 3 || !all[0].Allowed || !all[0].Default || all[1].Allowed || all[1].Default {
		t.Errorf("admin view = %+v; want every flow with only appFlow allowed and default", all)
	}
	members, err := f.svc.Flows(context.Background(), testOrg, false)
	if err != nil {
		t.Fatalf("Flows(members): %v", err)
	}
	if len(members) != 1 || members[0].ID != appFlow.ID {
		t.Errorf("member view = %+v; want only appFlow", members)
	}
}

func TestConfigureFlowsValidates(t *testing.T) {
	cases := map[string]struct {
		sel  FlowSelection
		want error
	}{
		"allowed with default": {FlowSelection{FlowIDs: []string{appFlow.ID, chipFlow.ID}, DefaultFlowID: chipFlow.ID}, nil},
		"none":                 {FlowSelection{}, nil},
		"no default":           {FlowSelection{FlowIDs: []string{appFlow.ID}}, ErrInvalidInput},
		"default not allowed":  {FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: chipFlow.ID}, ErrInvalidInput},
		"default without any":  {FlowSelection{DefaultFlowID: appFlow.ID}, ErrInvalidInput},
		"unknown flow":         {FlowSelection{FlowIDs: []string{"nope"}, DefaultFlowID: "nope"}, ErrFlowNotFound},
		"browser selfie":       {FlowSelection{FlowIDs: []string{browserFlow.ID}, DefaultFlowID: browserFlow.ID}, ErrFlowNotCompletable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			before := f.settings.selection
			err := f.svc.ConfigureFlows(context.Background(), testOrg, tc.sel)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want != nil && !slices.Equal(f.settings.selection.FlowIDs, before.FlowIDs) {
				t.Errorf("a refused selection was saved: %+v", f.settings.selection)
			}
		})
	}
}

func TestConfigureFlowsDropsDuplicates(t *testing.T) {
	f := newFixture()
	sel := FlowSelection{FlowIDs: []string{chipFlow.ID, chipFlow.ID}, DefaultFlowID: chipFlow.ID}
	if err := f.svc.ConfigureFlows(context.Background(), testOrg, sel); err != nil {
		t.Fatalf("ConfigureFlows: %v", err)
	}
	if !slices.Equal(f.settings.selection.FlowIDs, []string{chipFlow.ID}) {
		t.Errorf("saved = %v, want the flow once", f.settings.selection.FlowIDs)
	}
}

func TestCreateRequestValidates(t *testing.T) {
	cases := map[string]struct {
		in   NewRequest
		want error
	}{
		"not a member":   {NewRequest{SubjectUserID: uuid.New(), FlowID: appFlow.ID}, ErrMemberNotFound},
		"unknown flow":   {NewRequest{SubjectUserID: alex.UserID, FlowID: "nope"}, ErrFlowNotFound},
		"browser selfie": {NewRequest{SubjectUserID: alex.UserID, FlowID: browserFlow.ID}, ErrFlowNotCompletable},
		"not allowed":    {NewRequest{SubjectUserID: alex.UserID, FlowID: chipFlow.ID}, ErrFlowNotAllowed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New()}, tc.in)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(f.ips.sessions) != 0 || len(f.mailer.sent) != 0 {
				t.Errorf("a refused request created a session or sent mail")
			}
		})
	}
}

// Sending creates the IPS session on the request's id and flow, with the
// session TTL, and mails its vcmrtd deep link: the mail is the session.
func TestCreateRequestStartsTheSessionAndMailsItsDeepLink(t *testing.T) {
	f := newFixture()
	sent := f.send(t)

	if len(f.ips.sessions) != 1 || f.ips.sessions[0].TTL != SessionTTL ||
		f.ips.sessions[0].ClientReference != sent.Request.ID.String() || f.ips.sessions[0].FlowID != appFlow.ID {
		t.Fatalf("sessions = %+v, want one on the request id and flow, with the session TTL", f.ips.sessions)
	}
	stored := f.requests.created[0]
	if !stored.LinkExpiresAt.Equal(f.ips.sessionExpires) || stored.Subject.Email != alex.Email || stored.Flow.Version != 1 {
		t.Errorf("stored = %+v; want it to end with the session, sent to alex on the listed version", stored)
	}
	if len(f.requests.attached) != 1 {
		t.Errorf("attached = %+v, want the new session on the new request", f.requests.attached)
	}
	if at := sent.Request.SessionExpiresAt(time.Now()); at == nil || !at.Equal(f.ips.sessionExpires) ||
		sent.Request.FlowVersion != testPinnedVersion {
		t.Errorf("sent = %+v; want the session's deadline, on the pinned version", sent.Request)
	}
	if !sent.MailSent || len(f.mailer.sent) != 1 {
		t.Fatalf("mail sent = %v (%d mails), want one", sent.MailSent, len(f.mailer.sent))
	}
	mail := f.mailer.sent[0]
	if mail.to != alex.Email || mail.deepLink != "vcmrtd://verify?handover=ses-"+sent.Request.ID.String() || mail.validFor != SessionTTL {
		t.Errorf("mail = %+v, want the session's deep link, valid for the session TTL", mail)
	}
}

// A session IPS offers no vcmrtd link for cannot be mailed, so nothing is stored.
func TestCreateRequestWithoutADeepLinkStoresNothing(t *testing.T) {
	f := newFixture()
	f.ips.noClaim = true
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New()},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if err == nil || len(f.requests.created) != 0 || len(f.mailer.sent) != 0 {
		t.Errorf("err = %v, created = %d, mails = %d; want an error and nothing stored or sent",
			err, len(f.requests.created), len(f.mailer.sent))
	}
}

func TestCreateRequestReportsAFailedMail(t *testing.T) {
	f := newFixture()
	f.mailer.err = errors.New("smtp down")
	if sent := f.send(t); sent.MailSent {
		t.Error("a failed mail reported as sent")
	}
	if len(f.requests.created) != 1 {
		t.Error("the request was not stored")
	}
}

// A session IPS ends undecided (expired, or cancelled) ends the request with
// it: the mail was the session, so there is nothing to start again.
func TestASessionThatEndsUndecidedExpiresTheRequest(t *testing.T) {
	for _, status := range []proofingprovider.Status{proofingprovider.StatusExpired, proofingprovider.StatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture()
			f.send(t)
			f.ips.result = proofingprovider.Result{Status: status}
			req := f.reconcile(t)
			if f.requests.ended != 1 || req.SessionExpiresAt(time.Now()) != nil || req.EffectiveStatus(time.Now()) != StatusExpired {
				t.Errorf("ended = %d, request = %+v; want the session ended and the request expired", f.requests.ended, req)
			}
			f.reconcile(t)
			if f.requests.ended != 1 || len(f.ips.sessions) != 1 {
				t.Errorf("ended = %d, sessions = %d; want the end recorded once and no new session", f.requests.ended, len(f.ips.sessions))
			}
		})
	}
}

func TestAReviewIPSEndsExpiresTheRequest(t *testing.T) {
	f := newFixture()
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	if req := f.reconcile(t); req.Status != StatusNeedsReview || !req.needsReconcile() {
		t.Fatalf("request = %+v; want needs_review, still reconciled", req)
	}
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusExpired}
	req := f.reconcile(t)
	if f.requests.ended != 1 || req.EffectiveStatus(time.Now()) != StatusExpired || req.needsReconcile() {
		t.Errorf("ended = %d, request = %+v; want the review ended, expired and no longer reconciled", f.requests.ended, req)
	}
}

func TestReconcileMarksStartedThenRecordsOutcomeOnce(t *testing.T) {
	f := newFixture()
	f.send(t)

	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusOpened}
	for range 2 {
		f.reconcile(t)
	}
	if f.requests.started != 1 {
		t.Errorf("started = %d, want once", f.requests.started)
	}

	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, AssuranceLevel: "high", EIDASLevel: "substantial"}
	for range 2 {
		if req := f.reconcile(t); req.Status != StatusApproved {
			t.Fatalf("status = %s, want approved", req.Status)
		}
	}
	if len(f.requests.outcomes) != 1 {
		t.Errorf("outcomes recorded = %v, want one", f.requests.outcomes)
	}
}

func withRequiredAssurance(f proofingprovider.Flow, level string) proofingprovider.Flow {
	f.RequiredAssuranceLevel = level
	return f
}

// IPS approves on its checks alone; the wallet holds the approval to the
// level the flow demanded and rejects one that falls short.
func TestApprovalBelowRequiredAssuranceIsRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		required, achieved string
		want               Status
		wantCode           string
	}{
		"no requirement":      {"", "", StatusApproved, ""},
		"met exactly":         {"substantial", "substantial", StatusApproved, ""},
		"exceeded":            {"low", "substantial", StatusApproved, ""},
		"below":               {"substantial", "low", StatusRejected, ErrorAssuranceNotMet},
		"no level achieved":   {"low", "", StatusRejected, ErrorAssuranceNotMet},
		"unknown requirement": {"extreme", "substantial", StatusRejected, ErrorAssuranceNotMet},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			f.ips.flows[0] = withRequiredAssurance(appFlow, tc.required)
			f.send(t)
			f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: tc.achieved, Name: "Anna Jansen"}
			req := f.reconcile(t)
			if req.Status != tc.want || req.ErrorCode != tc.wantCode {
				t.Errorf("status = %s, code = %q; want %s, %q", req.Status, req.ErrorCode, tc.want, tc.wantCode)
			}
			if len(f.requests.outcomes) != 1 || f.requests.outcomes[0] != tc.want {
				t.Errorf("outcomes recorded = %v, want [%s]", f.requests.outcomes, tc.want)
			}
		})
	}
}

// A Yivi session reaches only yiviEIDASLevel: IPS does not score it, so the
// wallet records that level.
func TestYiviAssurance(t *testing.T) {
	f := newFixture()
	f.ips.flows[0] = withRequiredAssurance(appFlow, eidasLow)
	f.sendYivi(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, Method: proofingprovider.MethodYivi}
	if req := f.reconcile(t); req.Status != StatusApproved || req.EIDASLevel != yiviEIDASLevel {
		t.Errorf("status = %s, eIDAS = %q; want approved at %q", req.Status, req.EIDASLevel, yiviEIDASLevel)
	}
	if got := f.requests.results[0].EIDASLevel; got != yiviEIDASLevel {
		t.Errorf("recorded eIDAS level = %q, want %q", got, yiviEIDASLevel)
	}
}

func TestMeetsAssurance(t *testing.T) {
	for _, tc := range []struct {
		achieved, required string
		want               bool
	}{
		{"", "", true},
		{"low", "", true},
		{"low", "low", true},
		{"substantial", "low", true},
		{"high", "substantial", true},
		{"low", "substantial", false},
		{"", "low", false},
		{"bogus", "low", false},
		{"high", "bogus", false},
	} {
		if got := MeetsAssurance(tc.achieved, tc.required); got != tc.want {
			t.Errorf("MeetsAssurance(%q, %q) = %v, want %v", tc.achieved, tc.required, got, tc.want)
		}
	}
}

func TestRequestsSurviveIPSOutage(t *testing.T) {
	f := newFixture()
	f.send(t)
	f.ips.resultErr = errors.New("unreachable")
	if req := f.reconcile(t); req.Status != StatusPending {
		t.Errorf("status = %s; want the last known status and no error", req.Status)
	}
}

func TestEffectiveStatusExpiresUnfinishedOnly(t *testing.T) {
	over := &ipsSession{ID: "s", ExpiresAt: time.Now().Add(-time.Minute)}
	for status, want := range map[Status]Status{
		StatusPending: StatusExpired, StatusInProgress: StatusExpired,
		StatusApproved: StatusApproved, StatusNeedsReview: StatusNeedsReview,
	} {
		if got := (Request{Status: status, session: over}).EffectiveStatus(time.Now()); got != want {
			t.Errorf("%s past its session = %s, want %s", status, got, want)
		}
	}
	ended := time.Now()
	gaveUp := Request{Status: StatusNeedsReview, session: &ipsSession{ID: "s", ExpiresAt: time.Now().Add(-time.Minute), EndedAt: &ended}}
	if got := gaveUp.EffectiveStatus(time.Now()); got != StatusExpired {
		t.Errorf("a review IPS ended = %s, want expired", got)
	}
	running := Request{Status: StatusInProgress, session: &ipsSession{ID: "s", ExpiresAt: time.Now().Add(time.Minute)}}
	if got := running.EffectiveStatus(time.Now()); got != StatusInProgress {
		t.Errorf("a running session = %s, want in_progress", got)
	}
}

func TestCompletable(t *testing.T) {
	if !Completable(testFlow("a", "a", []string{"document_capture", "nfc_read"}, selfieLocationBrowser)) {
		t.Error("a flow without face steps needs no browser")
	}
	if Completable(testFlow("b", "b", []string{"nfc_read", "face_match"}, selfieLocationBrowser)) {
		t.Error("a browser face step cannot be reached by a recipient")
	}
	if Completable(testFlow("c", "c", []string{"face_verification"}, selfieLocationNative)) {
		t.Error("a face step without the chip needs a reference photo the wallet does not have")
	}
	if !Completable(testFlow("d", "d", []string{"document_capture", "nfc_read", "document_photo"}, "")) {
		t.Error("the Idem app photographs the document")
	}
}

func TestCreateAndEditFlowCaptureFaceInApp(t *testing.T) {
	f := newFixture()
	face := proofingprovider.FlowSpec{Name: "Face", Steps: []string{"document_capture", "nfc_read", "face_verification"}, SelfieLocation: selfieLocationBrowser}
	if _, err := f.svc.CreateFlow(context.Background(), testOrg, face); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	chip := proofingprovider.FlowSpec{Name: "Chip", Steps: []string{"document_capture", "nfc_read"}}
	edited, err := f.svc.EditFlow(context.Background(), testOrg, chipFlow.ID, chip)
	if err != nil || edited.Version != 2 {
		t.Fatalf("EditFlow = %+v, %v", edited, err)
	}
	if got := f.ips.createdFlows[0].SelfieLocation; got != selfieLocationNative {
		t.Errorf("face flow selfieLocation = %q, want native", got)
	}
	if got := f.ips.createdFlows[1].SelfieLocation; got != "" {
		t.Errorf("chip-only flow selfieLocation = %q, want none", got)
	}
	want := []string{audit.IdentityProofingFlowCreated, audit.IdentityProofingFlowVersionCreated}
	if !slices.Equal(f.settings.flowEvents, want) {
		t.Errorf("flow audits = %v, want %v", f.settings.flowEvents, want)
	}
}

func TestCreateFlowFaceProvider(t *testing.T) {
	f := newFixture()
	face := proofingprovider.FlowSpec{Name: "Face", Steps: []string{"document_capture", "nfc_read", "face_verification"}, FaceProvider: faceProviderIris}
	chip := proofingprovider.FlowSpec{Name: "Chip", Steps: []string{"document_capture", "nfc_read"}, FaceProvider: "regula"}
	for _, spec := range []proofingprovider.FlowSpec{face, chip} {
		if _, err := f.svc.CreateFlow(context.Background(), testOrg, spec); err != nil {
			t.Fatalf("CreateFlow(%s): %v", spec.Name, err)
		}
	}
	if got := f.ips.createdFlows[0].FaceProvider; got != faceProviderIris {
		t.Errorf("face flow faceProvider = %q, want Iris", got)
	}
	if got := f.ips.createdFlows[1].FaceProvider; got != "" {
		t.Errorf("chip-only flow faceProvider = %q, want none", got)
	}
	unset := face
	unset.FaceProvider = ""
	if _, err := f.svc.CreateFlow(context.Background(), testOrg, unset); err != nil {
		t.Fatalf("CreateFlow(no provider): %v", err)
	}
	if got := f.ips.createdFlows[2].FaceProvider; got != faceProviderRegula {
		t.Errorf("face flow without a provider = %q, want regula", got)
	}
	// The wallet runs no face engine of its own.
	for _, provider := range []string{"iris", faceProviderEngine} {
		bad := face
		bad.FaceProvider = provider
		if _, err := f.svc.CreateFlow(context.Background(), testOrg, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateFlow(%s) = %v, want ErrInvalidInput", provider, err)
		}
	}
}

func TestEditUnknownFlowIsNotFound(t *testing.T) {
	f := newFixture()
	_, err := f.svc.EditFlow(context.Background(), testOrg, "nope", proofingprovider.FlowSpec{Name: "x", Steps: []string{"nfc_read"}})
	if !errors.Is(err, ErrFlowNotFound) {
		t.Errorf("EditFlow = %v, want ErrFlowNotFound", err)
	}
}

func (f fixture) sendForCustomer(t *testing.T, email, name string) Sent {
	t.Helper()
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{CustomerID: &initech.ID, SubjectEmail: email, SubjectName: name, FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest for customer: %v", err)
	}
	return sent
}

// A customer's subject is not a member: it is reached by the address the sender
// typed, on a flow assigned to the customer even when members may not use it.
func TestCreateRequestForCustomerSubject(t *testing.T) {
	f := newFixture()
	sent := f.sendForCustomer(t, "  Anna@Example.ORG ", "  ")

	stored := f.requests.created[0].Subject
	if stored.UserID != nil || stored.CustomerID == nil || *stored.CustomerID != initech.ID {
		t.Errorf("subject = %+v, want initech's subject and no member", stored)
	}
	if stored.Email != "anna@example.org" || stored.Name != "" {
		t.Errorf("subject = %q <%s>, want no name and the normalised address", stored.Name, stored.Email)
	}
	if !sent.MailSent || f.mailer.sent[0].to != "anna@example.org" {
		t.Errorf("mail = %+v, want one to the subject", f.mailer.sent)
	}
}

func TestCreateRequestForCustomerValidates(t *testing.T) {
	unknown := uuid.New()
	cases := map[string]struct {
		in   NewRequest
		want error
	}{
		"unknown customer":   {NewRequest{CustomerID: &unknown, SubjectEmail: "a@example.org", FlowID: chipFlow.ID}, ErrCustomerNotFound},
		"bad address":        {NewRequest{CustomerID: &initech.ID, SubjectEmail: "not-an-address", FlowID: chipFlow.ID}, ErrInvalidInput},
		"member and subject": {NewRequest{CustomerID: &initech.ID, SubjectUserID: alex.UserID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID}, ErrInvalidInput},
		"not assigned":       {NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: appFlow.ID}, ErrFlowNotAssigned},
		"not completable":    {NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: browserFlow.ID}, ErrFlowNotCompletable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New()}, tc.in)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(f.ips.sessions) != 0 || len(f.mailer.sent) != 0 {
				t.Errorf("a refused request created a session or sent mail")
			}
		})
	}
}

// The app a subject used is carried from IPS's first report of the session
// opening onto the request, and kept once a later read no longer lists it.
func TestReconcileRecordsTheMethod(t *testing.T) {
	f := newFixture()
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusInProgress, Method: proofingprovider.MethodIdem}
	if got := f.reconcile(t); got.Method != proofingprovider.MethodIdem {
		t.Errorf("method after start = %q, want %q", got.Method, proofingprovider.MethodIdem)
	}
	if len(f.requests.methods) != 1 || f.requests.methods[0] != proofingprovider.MethodIdem {
		t.Errorf("stored methods = %v, want the Idem app once", f.requests.methods)
	}
}

// What the subject's app causes (joining the session, the outcome its evidence
// led to) is audited with that app as actor, whoever's read triggered it; an
// outcome after review is not the app's.
func TestReconcileAuditsTheAppAsActor(t *testing.T) {
	f := newFixture()
	f.send(t)
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{UserID: uuid.New()})
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusInProgress, Method: proofingprovider.MethodIdem}
	f.svc.SessionChanged(ctx, f.requests.stored.session.ID)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview, Method: proofingprovider.MethodIdem}
	f.svc.SessionChanged(ctx, f.requests.stored.session.ID)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusRejected, Method: proofingprovider.MethodIdem}
	f.svc.SessionChanged(ctx, f.requests.stored.session.ID)
	idem := SubjectAppActorPrefix + string(proofingprovider.MethodIdem)
	if want := []string{idem, idem, ""}; !slices.Equal(f.requests.actors, want) {
		t.Errorf("actors = %q, want %q", f.requests.actors, want)
	}
}

// A paused customer takes no new request, and resuming it takes them again.
func TestCreateRequestForPausedCustomerIsRefused(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	if _, err := f.svc.SetCustomerPaused(ctx, testOrg.ID, initech.ID, true); err != nil {
		t.Fatalf("SetCustomerPaused: %v", err)
	}
	in := NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID}
	if _, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()}, in); !errors.Is(err, ErrCustomerPaused) {
		t.Fatalf("err = %v, want %v", err, ErrCustomerPaused)
	}
	if len(f.ips.sessions) != 0 || len(f.mailer.sent) != 0 {
		t.Errorf("a refused request created a session or sent mail")
	}
	if _, err := f.svc.SetCustomerPaused(ctx, testOrg.ID, initech.ID, false); err != nil {
		t.Fatalf("SetCustomerPaused: %v", err)
	}
	if _, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()}, in); err != nil {
		t.Errorf("CreateRequest after resume: %v", err)
	}
}

// A customer without a live API key takes no live request; a test one runs.
func TestCreateRequestForCustomerWithoutLiveKeyIsRefused(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	c := f.customers.byID[initech.ID]
	c.HasLiveKey = false
	f.customers.byID[initech.ID] = c
	in := NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID}
	if _, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()}, in); !errors.Is(err, ErrCustomerNoAPIKey) {
		t.Fatalf("err = %v, want %v", err, ErrCustomerNoAPIKey)
	}
	if len(f.ips.sessions) != 0 || len(f.mailer.sent) != 0 {
		t.Errorf("a refused request created a session or sent mail")
	}
	in.Mode, in.ScriptedOutcome = ModeTest, "approve"
	if _, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()}, in); err != nil {
		t.Errorf("test CreateRequest: %v", err)
	}
}

// A customer's session lifetime is what IPS is asked for and what the mail says.
func TestCreateRequestForCustomerUsesItsSessionLifetime(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	short := CustomerSettings{SessionTTL: 2 * time.Minute, DataRetentionDays: 7}
	if _, err := f.svc.SaveCustomerSettings(ctx, testOrg.ID, initech.ID, short); err != nil {
		t.Fatalf("SaveCustomerSettings: %v", err)
	}
	f.sendForCustomer(t, "anna@example.org", "")
	if got := f.ips.sessions[0].TTL; got != short.SessionTTL {
		t.Errorf("session TTL = %v, want %v", got, short.SessionTTL)
	}
	if got := f.mailer.sent[0].validFor; got != short.SessionTTL {
		t.Errorf("mail validFor = %v, want %v", got, short.SessionTTL)
	}
}

func TestSaveCustomerSettingsOffersOnlyItsOptions(t *testing.T) {
	f := newFixture()
	for _, settings := range []CustomerSettings{
		{SessionTTL: 3 * time.Minute, DataRetentionDays: 30},
		{SessionTTL: SessionTTL, DataRetentionDays: 366},
		{SessionTTL: SessionTTL, DataRetentionDays: 60},
	} {
		if _, err := f.svc.SaveCustomerSettings(context.Background(), testOrg.ID, initech.ID, settings); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("SaveCustomerSettings(%+v) = %v, want ErrInvalidInput", settings, err)
		}
	}
}

// A customer's subject's mail is signed and styled as the customer; a member's
// stays the org's.
func TestCustomerSubjectMailCarriesTheCustomersBranding(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	logo := LogoChange{Replace: true, Logo: CustomerLogo{Bytes: []byte("\x89PNG"), ContentType: "image/png"}}
	if _, err := f.svc.SaveCustomerBranding(ctx, testOrg.ID, initech.ID, CustomerBranding{
		DisplayName: " Initech Verzekeringen ", PrimaryColor: "#1F5B4A",
		SupportContact: "help@initech.example", PrivacyURL: "https://initech.example/privacy",
	}, logo); err != nil {
		t.Fatalf("SaveCustomerBranding: %v", err)
	}
	f.sendForCustomer(t, "anna@example.org", "")
	f.send(t)

	customer, member := f.mailer.sent[0].mail, f.mailer.sent[1].mail
	if customer.OrgName != "Initech Verzekeringen" || customer.RequesterName != "Initech Verzekeringen" ||
		customer.SupportContact != "help@initech.example" || customer.PrivacyURL != "https://initech.example/privacy" ||
		customer.Brand == nil || customer.Brand.PrimaryColor != "#1F5B4A" || customer.Brand.Logo.ContentType != "image/png" {
		t.Errorf("customer mail = %+v; want Initech's name, contact, privacy statement, colour and logo", customer)
	}
	if member.OrgName != testOrg.Name || member.Brand != nil || member.SupportContact != "" {
		t.Errorf("member mail = %+v; want the org's, unbranded", member)
	}
}

func TestSaveCustomerBrandingValidates(t *testing.T) {
	f := newFixture()
	for name, b := range map[string]CustomerBranding{
		"colour":       {PrimaryColor: "green"},
		"http privacy": {PrivacyURL: "http://initech.example/privacy"},
		"relative":     {PrivacyURL: "/privacy"},
	} {
		if _, err := f.svc.SaveCustomerBranding(context.Background(), testOrg.ID, initech.ID, b, LogoChange{}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

// The proofed name is kept for a customer's approved subject only: a member has
// a name already, and a rejected document's name is not the subject's.
func TestReconcileKeepsProofedNameForApprovedCustomerSubjectOnly(t *testing.T) {
	cases := map[string]struct {
		customer bool
		outcome  proofingprovider.Status
		want     string
	}{
		"customer approved": {true, proofingprovider.StatusApproved, "Anna Jansen"},
		"customer rejected": {true, proofingprovider.StatusRejected, ""},
		"member approved":   {false, proofingprovider.StatusApproved, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			if tc.customer {
				f.sendForCustomer(t, "anna@example.org", "")
			} else {
				f.send(t)
			}
			f.ips.result = proofingprovider.Result{Status: tc.outcome, Name: "Anna Jansen"}
			req := f.reconcile(t)
			if !slices.Equal(f.requests.names, []string{tc.want}) || req.ProofedName != tc.want {
				t.Errorf("stored names = %q, shown %q; want %q", f.requests.names, req.ProofedName, tc.want)
			}
		})
	}
}

// Any flow of the org a recipient can finish may be assigned to a customer,
// including one members may not use.
func TestAssignCustomerFlowsValidates(t *testing.T) {
	cases := map[string]struct {
		sel  FlowSelection
		want error
	}{
		"not a members' flow": {FlowSelection{FlowIDs: []string{chipFlow.ID, appFlow.ID}, DefaultFlowID: appFlow.ID}, nil},
		"none":                {FlowSelection{}, nil},
		"no default":          {FlowSelection{FlowIDs: []string{chipFlow.ID}}, ErrInvalidInput},
		"unknown flow":        {FlowSelection{FlowIDs: []string{"nope"}, DefaultFlowID: "nope"}, ErrFlowNotFound},
		"browser selfie":      {FlowSelection{FlowIDs: []string{browserFlow.ID}, DefaultFlowID: browserFlow.ID}, ErrFlowNotCompletable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			_, err := f.svc.AssignCustomerFlows(context.Background(), testOrg, initech.ID, tc.sel)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if (tc.want == nil) != (len(f.customers.saved) == 1) {
				t.Errorf("saved = %+v; want a save only when accepted", f.customers.saved)
			}
		})
	}
	f := newFixture()
	if _, err := f.svc.AssignCustomerFlows(context.Background(), testOrg, uuid.New(), FlowSelection{}); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("unknown customer err = %v, want ErrCustomerNotFound", err)
	}
}

func TestCustomerFlowsAppliesAssignment(t *testing.T) {
	f := newFixture()
	all, err := f.svc.CustomerFlows(context.Background(), testOrg, initech.ID, true)
	if err != nil {
		t.Fatalf("CustomerFlows(all): %v", err)
	}
	if len(all) != 3 || all[0].Assigned || !all[1].Assigned || !all[1].Default {
		t.Errorf("admin view = %+v; want every flow with only chipFlow assigned and default", all)
	}
	assigned, err := f.svc.CustomerFlows(context.Background(), testOrg, initech.ID, false)
	if err != nil {
		t.Fatalf("CustomerFlows(assigned): %v", err)
	}
	if len(assigned) != 1 || assigned[0].ID != chipFlow.ID {
		t.Errorf("member view = %+v; want only chipFlow", assigned)
	}
}

func TestCreateCustomerNeedsAName(t *testing.T) {
	f := newFixture()
	for _, name := range []string{"", "   ", strings.Repeat("x", maxCustomerNameLength+1)} {
		if _, err := f.svc.CreateCustomer(context.Background(), testOrg.ID, uuid.New(), name); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateCustomer(%q) err = %v, want ErrInvalidInput", name, err)
		}
	}
	c, err := f.svc.CreateCustomer(context.Background(), testOrg.ID, uuid.New(), "  Globex  ")
	if err != nil || c.Name != "Globex" {
		t.Errorf("CreateCustomer = %+v, %v; want the trimmed name", c, err)
	}
}

func (f fixture) sendYivi(t *testing.T) Sent {
	t.Helper()
	f.ips.noClaim = true
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Method: proofingprovider.MethodYivi, Channel: ChannelOnScreen})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	return sent
}

// A customer's list pages newest first: a full page carries the cursor that
// continues after its last row, the last page none, a bad cursor is refused.
func TestCustomerRequestPage(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	base := time.Now()
	for i := range 3 {
		f.requests.page = append(f.requests.page, Request{ID: uuid.New(), CustomerID: &initech.ID, CreatedAt: base.Add(-time.Duration(i) * time.Minute)})
	}
	page, next, err := f.svc.CustomerRequestPage(ctx, testOrg.ID, initech.ID, "", 2)
	if err != nil || len(page) != 2 || next == "" {
		t.Fatalf("first page = %d rows, %q, %v; want 2 and a cursor", len(page), next, err)
	}
	if _, _, err := f.svc.CustomerRequestPage(ctx, testOrg.ID, initech.ID, next, 2); err != nil {
		t.Fatalf("next page: %v", err)
	}
	if got := f.requests.pages[1]; got == nil || got.ID != page[1].ID || !got.CreatedAt.Equal(page[1].CreatedAt) {
		t.Errorf("next page asked after %+v, want the first page's last row", got)
	}
	if _, next, _ := f.svc.CustomerRequestPage(ctx, testOrg.ID, initech.ID, "", 10); next != "" {
		t.Errorf("last page cursor = %q, want none", next)
	}
	if _, _, err := f.svc.CustomerRequestPage(ctx, testOrg.ID, initech.ID, "not-a-cursor", 2); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad cursor = %v, want %v", err, ErrInvalidInput)
	}
}

// A customer's result is read from IPS once settled, audited each time, and
// gone once erased.
func TestRequestResult(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()},
		NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if _, _, err := f.svc.RequestResult(ctx, testOrg.ID, initech.ID, sent.Request.ID); !errors.Is(err, ErrResultNotReady) {
		t.Errorf("RequestResult while pending = %v, want %v", err, ErrResultNotReady)
	}
	f.requests.stored.Status = StatusApproved
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
	_, identity, err := f.svc.RequestResult(ctx, testOrg.ID, initech.ID, sent.Request.ID)
	if err != nil || identity.FamilyName != "Jansen" || f.requests.resultReads != 1 {
		t.Fatalf("RequestResult = %+v, %v (%d audited); want IPS's identity, audited", identity, err, f.requests.resultReads)
	}
	now := time.Now()
	f.requests.stored.PurgedAt = &now
	if _, _, err := f.svc.RequestResult(ctx, testOrg.ID, initech.ID, sent.Request.ID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("RequestResult after erasure = %v, want %v", err, ErrRequestNotFound)
	}
}

// A new key gets every scope.
func TestCreateAPIKeyScopes(t *testing.T) {
	f := newFixture()
	key, _, err := f.svc.CreateAPIKey(context.Background(), testOrg.ID, initech.ID, uuid.New(), "CI", ModeLive)
	if err != nil || !slices.Equal(key.Scopes, APIKeyScopes) {
		t.Errorf("scopes = %v, %v; want every scope", key.Scopes, err)
	}
}

// A customer's request without an outcome is cancelled at IPS and here, once;
// purging it erases it at IPS and marks it.
func TestCancelAndPurgeRequest(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()},
		NewRequest{CustomerID: &initech.ID, SubjectEmail: "a@example.org", FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	req, err := f.svc.CancelRequest(ctx, testOrg.ID, initech.ID, sent.Request.ID)
	if err != nil || req.EffectiveStatus(time.Now()) != StatusCancelled || f.ips.cancels != 1 {
		t.Fatalf("CancelRequest = %v, %v (%d IPS cancels); want cancelled at IPS once", req.EffectiveStatus(time.Now()), err, f.ips.cancels)
	}
	if _, err := f.svc.CancelRequest(ctx, testOrg.ID, initech.ID, sent.Request.ID); !errors.Is(err, ErrSessionOver) || f.ips.cancels != 1 {
		t.Errorf("second CancelRequest = %v, want %v without IPS", err, ErrSessionOver)
	}
	if err := f.svc.PurgeRequest(ctx, testOrg.ID, initech.ID, sent.Request.ID); err != nil || f.ips.deletes != 1 || f.requests.stored.PurgedAt == nil {
		t.Fatalf("PurgeRequest = %v (%d IPS deletes); want erased at IPS and marked", err, f.ips.deletes)
	}
	if err := f.svc.PurgeRequest(ctx, testOrg.ID, initech.ID, sent.Request.ID); err != nil || f.ips.deletes != 1 {
		t.Errorf("second PurgeRequest = %v (%d deletes); want a no-op", err, f.ips.deletes)
	}
}

// A request past its retention is erased at IPS and here by the pruner; one
// IPS fails to erase is kept for the next run.
func TestPurgeDue(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New()},
		NewRequest{CustomerID: &initech.ID, SubjectName: "Anna", SubjectEmail: "a@example.org", FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if n, err := f.svc.PurgeDue(ctx); err != nil || n != 0 || f.ips.deletes != 0 {
		t.Fatalf("PurgeDue before retention = %d, %v (%d deletes); want nothing", n, err, f.ips.deletes)
	}
	past := time.Now().Add(-time.Minute)
	f.requests.stored.PurgeAt = &past
	f.ips.deleteErr = errors.New("ips down")
	if n, err := f.svc.PurgeDue(ctx); err != nil || n != 0 || f.requests.stored.PurgedAt != nil {
		t.Fatalf("PurgeDue with IPS down = %d, %v; want it kept", n, err)
	}
	f.ips.deleteErr = nil
	if n, err := f.svc.PurgeDue(ctx); err != nil || n != 1 || f.requests.stored.PurgedAt == nil {
		t.Fatalf("PurgeDue = %d, %v; want the request purged", n, err)
	}
	if got := f.requests.stored; got.SubjectName != "" || got.SubjectEmail != "" || got.ID != sent.Request.ID {
		t.Errorf("purged request = %+v; want no subject left", got)
	}
}

// A running Idem request gets a fresh app link from IPS, recorded as a handover
// only once a phone held the session; an app still holding the session, a Yivi
// request and a test request get none.
func TestClaimLink(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Method: proofingprovider.MethodIdem, Channel: ChannelOnScreen})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	claim, err := f.svc.ClaimLink(ctx, testOrg.ID, sent.Request.ID, nil)
	if err != nil || !strings.Contains(claim.DeepLink, "fresh-") || f.ips.handovers != 1 {
		t.Fatalf("ClaimLink = %+v, %v (%d calls); want IPS's fresh link", claim, err, f.ips.handovers)
	}
	if f.requests.handovers != 0 {
		t.Errorf("ClaimLink for an unscanned session recorded %d handovers, want 0", f.requests.handovers)
	}
	f.ips.slotClaimed = true
	if _, err := f.svc.ClaimLink(ctx, testOrg.ID, sent.Request.ID, nil); err != nil || f.requests.handovers != 1 {
		t.Errorf("ClaimLink after the phone left = %v, recorded %d handovers, want 1", err, f.requests.handovers)
	}
	f.ips.handoverErr = &proofingprovider.RejectedError{Status: http.StatusConflict, Code: proofingprovider.CodeDeviceActive}
	if _, err := f.svc.ClaimLink(ctx, testOrg.ID, sent.Request.ID, nil); !errors.Is(err, ErrDeviceActive) {
		t.Errorf("ClaimLink while the app is active = %v, want %v", err, ErrDeviceActive)
	}
	f.ips.handoverErr = &proofingprovider.RejectedError{Status: http.StatusGone, Code: "session_expired"}
	if _, err := f.svc.ClaimLink(ctx, testOrg.ID, sent.Request.ID, nil); !errors.Is(err, ErrSessionOver) {
		t.Errorf("ClaimLink after IPS ended it = %v, want %v", err, ErrSessionOver)
	}

	yivi := f.sendYivi(t)
	calls := f.ips.handovers
	if _, err := f.svc.ClaimLink(ctx, testOrg.ID, yivi.Request.ID, nil); !errors.Is(err, ErrWrongMethod) || f.ips.handovers != calls {
		t.Errorf("ClaimLink(Yivi) = %v, want %v without asking IPS", err, ErrWrongMethod)
	}
}

// A running Idem request's phone is read live from IPS, each time; a Yivi
// request has none.
func TestApp(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Method: proofingprovider.MethodIdem, Channel: ChannelOnScreen})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusInProgress, App: proofingprovider.AppAway}
	reads := f.ips.statusReads
	for range 2 {
		if app, err := f.svc.App(ctx, testOrg.ID, sent.Request.ID, nil); err != nil || app != proofingprovider.AppAway {
			t.Fatalf("App = %q, %v; want away", app, err)
		}
	}
	if f.ips.statusReads != reads+2 {
		t.Errorf("App read IPS %d times, want 2", f.ips.statusReads-reads)
	}
	f.ips.resultErr = errors.New("ips down")
	if _, err := f.svc.App(ctx, testOrg.ID, sent.Request.ID, nil); err == nil {
		t.Error("App with IPS down = nil error")
	}

	yivi := f.sendYivi(t)
	if _, err := f.svc.App(ctx, testOrg.ID, yivi.Request.ID, nil); !errors.Is(err, ErrWrongMethod) {
		t.Errorf("App(Yivi) = %v, want %v", err, ErrWrongMethod)
	}
}

// Only a face provider the Yivi app does not have keeps a flow from it; Regula
// and an unset provider run there.
func TestYiviNeedsAFaceProviderItHas(t *testing.T) {
	for provider, wantErr := range map[string]bool{
		faceProviderRegula: false, "": false, faceProviderIris: true,
	} {
		f := newFixture()
		f.ips.flows[0] = withFaceProvider(appFlow, provider)
		_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
			NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Method: proofingprovider.MethodYivi, Channel: ChannelOnScreen})
		if gotErr := errors.Is(err, ErrInvalidInput); gotErr != wantErr || !wantErr && err != nil {
			t.Errorf("CreateRequest(Yivi, provider %q) = %v, want refused %v", provider, err, wantErr)
		}
	}
	if !YiviAppAvailable(withFaceProvider(chipFlow, faceProviderIris)) {
		t.Error("a flow without a face step should run in the Yivi app")
	}
}

// The Yivi disclosure runs over OpenID4VP at the wallet's verifier; its photo
// goes to IPS as the face reference, apart from the other claims.
func TestYiviDisclosureRunsOverOpenID4VP(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	sent := f.sendYivi(t)

	started, err := f.svc.StartYivi(ctx, testOrg.ID, sent.Request.ID, nil)
	if err != nil {
		t.Fatalf("StartYivi: %v", err)
	}
	if !strings.HasPrefix(started.WalletLink, "openid4vp://") || !started.ExpiresAt.Equal(f.ips.sessionExpires) {
		t.Fatalf("started = %+v", started)
	}
	if f.requests.stored.yiviTransactionID != "tx-1" {
		t.Fatalf("transaction = %q, want it kept on the request", f.requests.stored.yiviTransactionID)
	}

	if _, err := f.svc.YiviDisclosure(ctx, testOrg.ID, sent.Request.ID, nil); !errors.Is(err, ErrDisclosurePending) {
		t.Fatalf("before the subject finished: %v, want ErrDisclosurePending", err)
	}
	if len(f.ips.references) != 0 {
		t.Fatal("a pending disclosure reached IPS")
	}

	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"idcard": {openid4vpverifier.ClaimGivenNames: "Anna", openid4vpverifier.ClaimPhoto: "cGhvdG8="},
	}}
	disclosure, err := f.svc.YiviDisclosure(ctx, testOrg.ID, sent.Request.ID, nil)
	if err != nil || !disclosure.OK {
		t.Fatalf("YiviDisclosure = %+v, %v", disclosure, err)
	}
	if got := f.verifier.polled[len(f.verifier.polled)-1]; got != "tx-1" {
		t.Fatalf("polled %q, want the request's transaction", got)
	}
	want := proofingprovider.Reference{
		Credential: "pbdf-staging.pbdf.idcard", Photo: "cGhvdG8=",
		Attributes: map[string]string{openid4vpverifier.ClaimGivenNames: "Anna"},
	}
	if len(f.ips.references) != 1 || f.ips.references[0].Credential != want.Credential ||
		f.ips.references[0].Photo != want.Photo || !maps.Equal(f.ips.references[0].Attributes, want.Attributes) {
		t.Fatalf("references = %+v, want %+v", f.ips.references, want)
	}
}

func TestYiviStepsRefuseAnIdemRequest(t *testing.T) {
	f := newFixture()
	sent := f.send(t)
	if _, err := f.svc.StartYivi(context.Background(), testOrg.ID, sent.Request.ID, nil); !errors.Is(err, ErrWrongMethod) {
		t.Fatalf("StartYivi on an Idem request: %v, want ErrWrongMethod", err)
	}
	if f.verifier.started != 0 {
		t.Fatal("a presentation was started for an Idem request")
	}
}

func TestListReadsNeverCallIPS(t *testing.T) {
	f := newFixture()
	f.send(t)
	for range 3 {
		if _, err := f.svc.Requests(context.Background(), testOrg.ID, RequestFilter{}); err != nil {
			t.Fatalf("Requests: %v", err)
		}
	}
	if f.ips.statusReads+f.ips.resultReads != 0 {
		t.Errorf("IPS reads = %d, want none: outcomes are pushed", f.ips.statusReads+f.ips.resultReads)
	}
}

func TestARequestReadRechecksIPSAtMostOncePerInterval(t *testing.T) {
	f := newFixture()
	sent := f.send(t)
	now := time.Now()
	f.svc.now = func() time.Time { return now }
	read := func() {
		t.Helper()
		if _, err := f.svc.Request(context.Background(), testOrg.ID, sent.Request.ID, nil); err != nil {
			t.Fatalf("Request: %v", err)
		}
	}
	read()
	read()
	if f.ips.statusReads != 1 {
		t.Fatalf("status reads = %d, want 1 within the interval", f.ips.statusReads)
	}
	now = now.Add(readReconcileEvery)
	read()
	if f.ips.statusReads != 2 {
		t.Errorf("status reads = %d, want 2 after the interval", f.ips.statusReads)
	}
}

func TestCreateRequestResolvesEachDependencyOnce(t *testing.T) {
	f := newFixture()
	customer := initech
	customer.Flows = FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}
	f.customers.byID[customer.ID] = customer
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{CustomerID: &customer.ID, SubjectEmail: "anna@example.org"})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if f.ips.flowLists != 1 || f.customers.gets != 1 || len(f.ips.sessions) != 1 {
		t.Errorf("flow lists = %d, customer reads = %d, sessions = %d; want 1 each",
			f.ips.flowLists, f.customers.gets, len(f.ips.sessions))
	}
}

// Reconciling reads the status only; the full result, with personal data, is
// read just for an approved customer subject, whose name is kept.
func TestReconcileReadsTheFullResultOnlyForTheName(t *testing.T) {
	for name, tc := range map[string]struct {
		customer bool
		status   proofingprovider.Status
		want     int
	}{
		"member approved":           {false, proofingprovider.StatusApproved, 0},
		"customer subject rejected": {true, proofingprovider.StatusRejected, 0},
		"customer subject approved": {true, proofingprovider.StatusApproved, 1},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			if tc.customer {
				customer := initech
				customer.Flows = FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}
				f.customers.byID[customer.ID] = customer
				if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
					NewRequest{CustomerID: &customer.ID, SubjectEmail: "anna@example.org"}); err != nil {
					t.Fatalf("CreateRequest: %v", err)
				}
			} else {
				f.send(t)
			}
			f.ips.result = proofingprovider.Result{Status: tc.status, Name: "Anna Jansen"}
			f.reconcile(t)
			if f.ips.resultReads != tc.want {
				t.Errorf("full result reads = %d, want %d", f.ips.resultReads, tc.want)
			}
		})
	}
}

// testCustomer is initech with appFlow assigned as its default.
func (f fixture) testCustomer() Customer {
	customer := initech
	customer.Flows = FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}
	f.customers.byID[customer.ID] = customer
	return customer
}

func TestATestRequestRunsScriptedOnTheTestKey(t *testing.T) {
	f := newFixture()
	customer := f.testCustomer()
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
	for range 2 {
		sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "CI key", APIKeyID: new(uuid.UUID)},
			NewRequest{CustomerID: &customer.ID, SubjectEmail: "anna@example.org", Mode: ModeTest})
		if err != nil {
			t.Fatalf("CreateRequest(test): %v", err)
		}
		if sent.Request.Status != StatusApproved || sent.Request.Mode != ModeTest || sent.MailSent || sent.DeepLink != "" {
			t.Errorf("sent = %+v; want an approved, unmailed test request", sent)
		}
	}
	in := f.ips.sessions[0]
	if f.ips.sessionKeys[0] != orgTenant(testOrg.ID, ModeTest) || in.FlowID != "" || in.ScriptedOutcome != defaultScriptedOutcome {
		t.Errorf("session on tenant %+v with %+v; want the org in test mode, no flow, scripted %q",
			f.ips.sessionKeys[0], in, defaultScriptedOutcome)
	}
	if len(f.mailer.sent) != 0 {
		t.Errorf("mails = %d, want none for a test request", len(f.mailer.sent))
	}
}

func TestOnlyATestRequestScriptsAValidOutcome(t *testing.T) {
	f := newFixture()
	customer := f.testCustomer()
	for name, in := range map[string]NewRequest{
		"live with a script":  {ScriptedOutcome: "approve"},
		"unknown script":      {Mode: ModeTest, ScriptedOutcome: "approve-all"},
		"reject without code": {Mode: ModeTest, ScriptedOutcome: "reject:"},
	} {
		in.CustomerID, in.SubjectEmail = &customer.ID, "anna@example.org"
		if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "key"}, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestWebhookDataCarriesLivemode(t *testing.T) {
	if live := sessionEventData(Request{}, StatusApproved)["livemode"]; live != true {
		t.Errorf("live request livemode = %v, want true", live)
	}
	if test := sessionEventData(Request{Mode: ModeTest}, StatusApproved)["livemode"]; test != false {
		t.Errorf("test request livemode = %v, want false", test)
	}
}

const testHostedBase = "https://wallet.test/p/"

// sendHosted creates a hosted request for initech's subject and returns its
// link's token.
func (f fixture) sendHosted(t *testing.T) (Sent, string) {
	t.Helper()
	customer := f.testCustomer()
	f.svc.SetHostedBaseURL(testHostedBase)
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{CustomerID: &customer.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	token, ok := strings.CutPrefix(sent.HostedURL, testHostedBase)
	if !ok || len(token) < linkTokenBytes {
		t.Fatalf("hosted url = %q, want %s<token>", sent.HostedURL, testHostedBase)
	}
	return sent, token
}

func TestAHostedLinkStartsOneSessionWhenTheSubjectPicksAnApp(t *testing.T) {
	f := newFixture()
	sent, token := f.sendHosted(t)
	if len(f.ips.sessions) != 0 || len(f.mailer.sent) != 0 || sent.DeepLink != "" {
		t.Fatalf("a hosted request made %d sessions and %d mails; want none until the subject starts",
			len(f.ips.sessions), len(f.mailer.sent))
	}
	if got := sent.Request.EffectiveStatus(time.Now()); got != StatusPending {
		t.Errorf("unstarted link status = %s, want pending", got)
	}
	view, err := f.svc.HostedRequest(context.Background(), token)
	if err != nil || view.Flow.ID != appFlow.ID || view.Customer.ID != initech.ID {
		t.Fatalf("HostedRequest = %+v, %v; want the flow and customer", view, err)
	}
	started, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem)
	if err != nil || started.DeepLink == "" || len(f.ips.sessions) != 1 {
		t.Fatalf("StartHosted = %+v, %v; want one session with its deep link", started, err)
	}
	if in := f.ips.sessions[0]; in.FlowID != appFlow.ID || in.ClientReference != sent.Request.ID.String() {
		t.Errorf("session input = %+v; want the request's flow and id", in)
	}
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); !errors.Is(err, ErrLinkStarted) {
		t.Errorf("second start = %v, want ErrLinkStarted", err)
	}
	if _, err := f.svc.HostedRequest(context.Background(), token+"x"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("unknown token = %v, want ErrRequestNotFound", err)
	}
}

func TestAHostedLinkCannotBeStartedOnceItExpired(t *testing.T) {
	f := newFixture()
	sent, token := f.sendHosted(t)
	later := time.Now().Add(HostedLinkTTL + time.Minute)
	f.svc.now = func() time.Time { return later }
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); !errors.Is(err, ErrSessionOver) {
		t.Errorf("start after the link expired = %v, want ErrSessionOver", err)
	}
	if got := sent.Request.EffectiveStatus(later); got != StatusExpired {
		t.Errorf("status after the link expired = %s, want expired", got)
	}
}

func TestAHostedLinkRunsTheYiviAppFromTheSubjectsBrowser(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodYivi); err != nil {
		t.Fatalf("StartHosted(yivi): %v", err)
	}
	started, err := f.svc.HostedStartYivi(context.Background(), token)
	if err != nil || started.WalletLink == "" || f.verifier.started != 1 {
		t.Errorf("HostedStartYivi = %+v, %v; want a started presentation", started, err)
	}
}

func TestAHostedRequestNeedsACustomerAndALiveKey(t *testing.T) {
	f := newFixture()
	customer := f.testCustomer()
	f.svc.SetHostedBaseURL(testHostedBase)
	for name, in := range map[string]NewRequest{
		"member":    {SubjectUserID: alex.UserID, FlowID: appFlow.ID, Channel: ChannelHosted},
		"test mode": {CustomerID: &customer.ID, Channel: ChannelHosted, Mode: ModeTest},
	} {
		if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "x"}, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestAReviewDecisionSettlesTheRequestThroughItsOutcome(t *testing.T) {
	f := newFixture()
	sent := f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", req.Status)
	}
	for name, in := range map[string]ReviewInput{
		"no reason":           {Approve: true},
		"code on an approval": {Approve: true, Reason: "ok", ErrorCode: "X"},
		"malformed code":      {Reason: "no", ErrorCode: "not ok"},
	} {
		if _, err := f.svc.DecideReview(context.Background(), testOrg.ID, sent.Request.ID, "sam@acme.test", in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
	req, err := f.svc.DecideReview(context.Background(), testOrg.ID, sent.Request.ID, "sam@acme.test",
		ReviewInput{Approve: true, Reason: "document checked by hand"})
	if err != nil || req.Status != StatusApproved {
		t.Fatalf("DecideReview = %+v, %v; want approved", req, err)
	}
	if len(f.ips.decisions) != 1 || f.ips.decisions[0].Reviewer != "sam@acme.test" ||
		len(f.requests.reviews) != 1 || f.requests.reviews[0] != "approved: document checked by hand" {
		t.Errorf("decisions = %+v, audited %v; want one, by sam, with its reason", f.ips.decisions, f.requests.reviews)
	}
	if _, err := f.svc.DecideReview(context.Background(), testOrg.ID, sent.Request.ID, "sam@acme.test",
		ReviewInput{Reason: "again"}); !errors.Is(err, ErrNotUnderReview) {
		t.Errorf("a second decision = %v, want ErrNotUnderReview", err)
	}
}
