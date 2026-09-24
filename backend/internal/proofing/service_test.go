package proofing

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const (
	testAPIKey = "sk_live_org"
)

type fakeSettings struct {
	canStore   bool
	apiKey     string
	saves      int
	selection  FlowSelection
	flowEvents []string
}

func (f *fakeSettings) CanStoreSecrets() bool { return f.canStore }

func (f *fakeSettings) APIKey(context.Context, uuid.UUID) (string, error) {
	if f.apiKey == "" {
		return "", ErrNotProvisioned
	}
	return f.apiKey, nil
}

func (f *fakeSettings) Save(_ context.Context, _ uuid.UUID, _, apiKey, _ string) (bool, error) {
	f.saves++
	f.apiKey = apiKey
	return true, nil
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
	members map[uuid.UUID]Member
	// stored is the last request created.
	stored   *Request
	created  []NewStoredRequest
	attached []proofingprovider.Session
	outcomes []Status
	names    []string
	started  int
	ended    int
}

func (f *fakeRequests) Create(_ context.Context, in NewStoredRequest) (Request, error) {
	f.created = append(f.created, in)
	req := Request{
		ID: in.ID, OrganizationID: in.OrgID, SubjectUserID: in.Subject.UserID, CustomerID: in.Subject.CustomerID,
		SubjectName:  in.Subject.Name,
		SubjectEmail: in.Subject.Email, FlowID: in.Flow.ID, FlowName: in.Flow.Name, FlowVersion: in.Flow.Version,
		Status: StatusPending, LinkExpiresAt: in.LinkExpiresAt,
	}
	f.stored = &req
	return req, nil
}

func (f *fakeRequests) AttachSession(_ context.Context, _ Request, sess proofingprovider.Session) (bool, error) {
	f.attached = append(f.attached, sess)
	f.stored.session = &ipsSession{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt}
	f.stored.FlowVersion = sess.FlowVersion
	return true, nil
}

func (f *fakeRequests) List(context.Context, uuid.UUID, RequestFilter) ([]Request, error) {
	if f.stored == nil {
		return []Request{}, nil
	}
	return []Request{*f.stored}, nil
}

func (f *fakeRequests) MarkStarted(context.Context, Request, string) error {
	f.started++
	f.stored.Status = StatusInProgress
	return nil
}

func (f *fakeRequests) EndSession(context.Context, Request, string, proofingprovider.Status) error {
	f.ended++
	now := time.Now()
	f.stored.session.EndedAt = &now
	return nil
}

func (f *fakeRequests) Member(_ context.Context, _, userID uuid.UUID) (Member, error) {
	m, ok := f.members[userID]
	if !ok {
		return Member{}, ErrMemberNotFound
	}
	return m, nil
}

func (f *fakeRequests) RecordOutcome(_ context.Context, _ Request, _ string, status Status, res proofingprovider.Result) error {
	f.outcomes = append(f.outcomes, status)
	f.names = append(f.names, res.Name)
	f.stored.Status = status
	f.stored.ProofedName = res.Name
	return nil
}

type fakeCustomers struct {
	byID  map[uuid.UUID]Customer
	saved []FlowSelection
}

func (f *fakeCustomers) List(context.Context, uuid.UUID) ([]Customer, error) {
	out := []Customer{}
	for _, c := range f.byID {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeCustomers) Get(_ context.Context, _, id uuid.UUID) (Customer, error) {
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

type fakeIPS struct {
	flows          []proofingprovider.Flow
	result         proofingprovider.Result
	resultErr      error
	tenants        int
	sessions       []proofingprovider.SessionInput
	sessionExpires time.Time
	createdFlows   []proofingprovider.FlowSpec
	versions       []string
	// noClaim makes a created session come without a vcmrtd link.
	noClaim bool
}

func (f *fakeIPS) CreateTenant(context.Context, string) (proofingprovider.Tenant, error) {
	f.tenants++
	return proofingprovider.Tenant{ID: "t1", WebhookSecret: "whsec"}, nil
}

func (*fakeIPS) CreateAPIKey(context.Context, string, []string) (string, error) {
	return testAPIKey, nil
}

func (f *fakeIPS) ListFlows(context.Context, string) ([]proofingprovider.Flow, error) {
	return f.flows, nil
}

func (f *fakeIPS) CreateFlow(_ context.Context, _ string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	f.createdFlows = append(f.createdFlows, in)
	return proofingprovider.Flow{FlowSpec: in, ID: "new", Version: 1, Active: true}, nil
}

func (f *fakeIPS) CreateFlowVersion(_ context.Context, _, id string, in proofingprovider.FlowSpec) (proofingprovider.Flow, error) {
	if !slices.ContainsFunc(f.flows, func(fl proofingprovider.Flow) bool { return fl.ID == id }) {
		return proofingprovider.Flow{}, proofingprovider.ErrNotFound
	}
	f.versions = append(f.versions, id)
	f.createdFlows = append(f.createdFlows, in)
	return proofingprovider.Flow{FlowSpec: in, ID: id, Version: 2, Active: true}, nil
}

func (f *fakeIPS) ListFlowVersions(_ context.Context, _, id string) ([]proofingprovider.Flow, error) {
	return []proofingprovider.Flow{{ID: id, Version: 1}, {ID: id, Version: 2, Active: true}}, nil
}

func (*fakeIPS) ActivateFlowVersion(_ context.Context, _, id string, version int) (proofingprovider.Flow, error) {
	return proofingprovider.Flow{ID: id, Version: version, Active: true}, nil
}

func (f *fakeIPS) CreateSession(_ context.Context, _ string, in proofingprovider.SessionInput) (proofingprovider.Session, error) {
	f.sessions = append(f.sessions, in)
	id := "ses-" + in.ClientReference
	sess := proofingprovider.Session{ID: id, Token: "tok", ExpiresAt: f.sessionExpires, FlowVersion: testPinnedVersion}
	if !f.noClaim {
		sess.Claim = &proofingprovider.Claim{DeepLink: "vcmrtd://verify?handover=" + id, ExpiresAt: time.Now().Add(time.Minute)}
	}
	return sess, nil
}

func (f *fakeIPS) SessionResult(context.Context, string, string, string) (proofingprovider.Result, error) {
	return f.result, f.resultErr
}

type sentMail struct {
	to, requester, deepLink string
	validFor                time.Duration
}

type fakeMailer struct {
	sent []sentMail
	err  error
}

func (f *fakeMailer) SendIdentityProofingRequested(_ context.Context, _ uuid.UUID, to, _, requester, deepLink string, validFor time.Duration) error {
	f.sent = append(f.sent, sentMail{to: to, requester: requester, deepLink: deepLink, validFor: validFor})
	return f.err
}

func testFlow(id, name string, steps []string, selfieLocation string) proofingprovider.Flow {
	return proofingprovider.Flow{
		FlowSpec: proofingprovider.FlowSpec{Name: name, Steps: steps, SelfieLocation: selfieLocation},
		ID:       id, Version: 1, Active: true,
	}
}

var (
	appFlow  = testFlow("f-app", "Passport + face", []string{"nfc_read", "face_match"}, "native")
	chipFlow = testFlow("f-chip", "Passport only", []string{"document_capture", "nfc_read"}, "native")
	// browserFlow captures the face in a browser a recipient cannot reach.
	browserFlow = testFlow("f-web", "Web selfie", []string{"selfie"}, selfieLocationBrowser)

	testOrg = Org{ID: uuid.New(), Name: "Acme BV"}
	alex    = Member{UserID: uuid.New(), Name: "Alex Jansen", Email: "alex@example.org", Role: "admin", MemberType: "employee"}
	// initech is a customer with chipFlow assigned (a flow members may not use)
	// as its default.
	initech     = Customer{ID: uuid.New(), OrganizationID: testOrg.ID, Name: "Initech", Flows: FlowSelection{FlowIDs: []string{chipFlow.ID}, DefaultFlowID: chipFlow.ID}}
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
	mailer    *fakeMailer
}

// newFixture builds a service over fakes. A provisioned org already has its IPS
// key and appFlow made available to members as the default; alex is a member
// and initech a customer.
func newFixture(provisioned bool) fixture {
	f := fixture{
		settings: &fakeSettings{canStore: true, selection: FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}},
		requests: &fakeRequests{members: map[uuid.UUID]Member{alex.UserID: alex}},
		ips: &fakeIPS{
			flows: []proofingprovider.Flow{appFlow, chipFlow, browserFlow}, sessionExpires: time.Now().Add(testSession),
			result: proofingprovider.Result{Status: proofingprovider.StatusCreated},
		},
		customers: &fakeCustomers{byID: map[uuid.UUID]Customer{initech.ID: initech}},
		mailer:    &fakeMailer{},
	}
	if provisioned {
		f.settings.apiKey = testAPIKey
	}
	f.svc = NewService(f.settings, f.requests, f.customers, f.ips, f.mailer)
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

// reconcile is a request list read, which re-checks the sent request at IPS.
func (f fixture) reconcile(t *testing.T) Request {
	t.Helper()
	reqs, err := f.svc.Requests(context.Background(), testOrg.ID, RequestFilter{})
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Requests = %+v, %v; want the one request", reqs, err)
	}
	return reqs[0]
}

func TestFirstUseProvisionsOnce(t *testing.T) {
	f := newFixture(false)
	for range 2 {
		if _, err := f.svc.Flows(context.Background(), testOrg, true); err != nil {
			t.Fatalf("Flows: %v", err)
		}
	}
	if f.ips.tenants != 1 || f.settings.saves != 1 {
		t.Errorf("tenants = %d, saves = %d; want one of each", f.ips.tenants, f.settings.saves)
	}
}

func TestFirstUseWithoutKeyProvisionsNothing(t *testing.T) {
	f := newFixture(false)
	f.settings.canStore = false
	if _, err := f.svc.Flows(context.Background(), testOrg, true); !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("Flows = %v, want ErrNoEncryptionKey", err)
	}
	if f.ips.tenants != 0 {
		t.Errorf("an IPS tenant was created with nowhere to store its key")
	}
}

func TestFlowsAppliesSelection(t *testing.T) {
	f := newFixture(true)
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
			f := newFixture(true)
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
	f := newFixture(true)
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
			f := newFixture(true)
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
	f := newFixture(true)
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
	f := newFixture(true)
	f.ips.noClaim = true
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New()},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if err == nil || len(f.requests.created) != 0 || len(f.mailer.sent) != 0 {
		t.Errorf("err = %v, created = %d, mails = %d; want an error and nothing stored or sent",
			err, len(f.requests.created), len(f.mailer.sent))
	}
}

func TestCreateRequestReportsAFailedMail(t *testing.T) {
	f := newFixture(true)
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
			f := newFixture(true)
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

func TestReconcileMarksStartedThenRecordsOutcomeOnce(t *testing.T) {
	f := newFixture(true)
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

func TestRequestsSurviveIPSOutage(t *testing.T) {
	f := newFixture(true)
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
}

func TestCreateAndEditFlowCaptureFaceInApp(t *testing.T) {
	f := newFixture(true)
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

func TestEditUnknownFlowIsNotFound(t *testing.T) {
	f := newFixture(true)
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
	f := newFixture(true)
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
			f := newFixture(true)
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
			f := newFixture(true)
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
			f := newFixture(true)
			_, err := f.svc.AssignCustomerFlows(context.Background(), testOrg, initech.ID, tc.sel)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if (tc.want == nil) != (len(f.customers.saved) == 1) {
				t.Errorf("saved = %+v; want a save only when accepted", f.customers.saved)
			}
		})
	}
	f := newFixture(true)
	if _, err := f.svc.AssignCustomerFlows(context.Background(), testOrg, uuid.New(), FlowSelection{}); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("unknown customer err = %v, want ErrCustomerNotFound", err)
	}
}

func TestCustomerFlowsAppliesAssignment(t *testing.T) {
	f := newFixture(true)
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
	f := newFixture(true)
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
