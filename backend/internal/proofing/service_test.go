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
	testAPIKey     = "sk_live_org"
	testAppBaseURL = "https://wallet.example.org/"
	testRawToken   = "raw-token"
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
	members  map[uuid.UUID]Member
	byToken  map[string]*Request
	created  []NewStoredRequest
	outcomes []Status
	started  int
	expired  int
}

func (f *fakeRequests) Create(_ context.Context, in NewStoredRequest) (Request, string, error) {
	f.created = append(f.created, in)
	subject := in.Subject.UserID
	req := Request{
		ID: in.ID, OrganizationID: in.OrgID, SubjectUserID: &subject, SubjectName: in.Subject.Name,
		SubjectEmail: in.Subject.Email, FlowID: in.Flow.ID, FlowName: in.Flow.Name, FlowVersion: in.Flow.Version,
		Status: StatusPending, LinkExpiresAt: in.LinkExpiresAt,
		session: &ipsSession{ID: in.Session.ID, Token: in.Session.Token, ExpiresAt: in.Session.ExpiresAt},
	}
	f.byToken[testRawToken] = &req
	return req, testRawToken, nil
}

func (f *fakeRequests) List(context.Context, uuid.UUID, *uuid.UUID) ([]Request, error) {
	out := []Request{}
	for _, r := range f.byToken {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeRequests) ByToken(_ context.Context, raw string) (Link, error) {
	r, ok := f.byToken[raw]
	if !ok || !time.Now().Before(r.LinkExpiresAt) {
		return Link{}, ErrLinkNotFound
	}
	return Link{Request: *r, OrganizationName: "Acme BV"}, nil
}

func (f *fakeRequests) MarkStarted(context.Context, Request, string) error {
	f.started++
	f.byToken[testRawToken].Status = StatusInProgress
	return nil
}

func (f *fakeRequests) ExpireLink(context.Context, uuid.UUID, string) error {
	f.expired++
	f.byToken[testRawToken].LinkExpiresAt = time.Now()
	return nil
}

func (f *fakeRequests) Members(context.Context, uuid.UUID) ([]Member, error) {
	out := []Member{}
	for _, m := range f.members {
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeRequests) Member(_ context.Context, _, userID uuid.UUID) (Member, error) {
	m, ok := f.members[userID]
	if !ok {
		return Member{}, ErrMemberNotFound
	}
	return m, nil
}

func (f *fakeRequests) RecordOutcome(_ context.Context, _ Request, _ string, status Status, _ proofingprovider.Result) error {
	f.outcomes = append(f.outcomes, status)
	f.byToken[testRawToken].Status = status
	return nil
}

type fakeIPS struct {
	flows          []proofingprovider.Flow
	result         proofingprovider.Result
	resultErr      error
	tenants        int
	sessions       []proofingprovider.SessionInput
	claims         int
	sessionExpires time.Time
	createdFlows   []proofingprovider.FlowSpec
	versions       []string
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
	return proofingprovider.Session{
		ID: id, Token: "tok", ExpiresAt: f.sessionExpires,
		Claim: &proofingprovider.Claim{DeepLink: "vcmrtd://verify?handover=" + id, ExpiresAt: time.Now().Add(time.Minute)},
	}, nil
}

func (f *fakeIPS) MintClaim(context.Context, string, string, string) (*proofingprovider.Claim, error) {
	f.claims++
	return &proofingprovider.Claim{DeepLink: "vcmrtd://verify?handover=fresh", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (f *fakeIPS) SessionResult(context.Context, string, string, string) (proofingprovider.Result, error) {
	return f.result, f.resultErr
}

type sentMail struct {
	to, requester, url string
	validFor           time.Duration
}

type fakeMailer struct {
	sent []sentMail
	err  error
}

func (f *fakeMailer) SendIdentityProofingRequested(_ context.Context, _ uuid.UUID, to, _, requester, url string, validFor time.Duration) error {
	f.sent = append(f.sent, sentMail{to: to, requester: requester, url: url, validFor: validFor})
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

	testOrg     = Org{ID: uuid.New(), Name: "Acme BV"}
	alex        = Member{UserID: uuid.New(), Name: "Alex Jansen", Email: "alex@example.org", Role: "admin", MemberType: "employee"}
	testSession = 15 * time.Minute
)

type fixture struct {
	svc      *Service
	settings *fakeSettings
	requests *fakeRequests
	ips      *fakeIPS
	mailer   *fakeMailer
}

// newFixture builds a service over fakes. A provisioned org already has its IPS
// key and appFlow made available to members as the default; alex is a member.
func newFixture(provisioned bool) fixture {
	f := fixture{
		settings: &fakeSettings{canStore: true, selection: FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}},
		requests: &fakeRequests{members: map[uuid.UUID]Member{alex.UserID: alex}, byToken: map[string]*Request{}},
		ips: &fakeIPS{
			flows: []proofingprovider.Flow{appFlow, chipFlow, browserFlow}, sessionExpires: time.Now().Add(testSession),
			result: proofingprovider.Result{Status: proofingprovider.StatusCreated},
		},
		mailer: &fakeMailer{},
	}
	if provisioned {
		f.settings.apiKey = testAPIKey
	}
	f.svc = NewService(f.settings, f.requests, f.ips, f.mailer, testAppBaseURL)
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

// The mailed link and the IPS session are one lifetime: the session is created
// at send, and the link expires when it does.
func TestCreateRequestStartsSessionAndMailsLinkWithItsLifetime(t *testing.T) {
	f := newFixture(true)
	sent := f.send(t)

	if len(f.ips.sessions) != 1 || f.ips.sessions[0].TTL != SessionTTL || f.ips.sessions[0].ClientReference != sent.Request.ID.String() {
		t.Fatalf("sessions = %+v, want one on the request id with the session TTL", f.ips.sessions)
	}
	stored := f.requests.created[0]
	if !stored.LinkExpiresAt.Equal(f.ips.sessionExpires) || stored.Subject.Email != alex.Email || stored.Flow.Version != 1 {
		t.Errorf("stored = %+v; want the link to expire with the session, sent to alex on the pinned version", stored)
	}
	if !sent.MailSent || len(f.mailer.sent) != 1 {
		t.Fatalf("mail sent = %v (%d mails), want one", sent.MailSent, len(f.mailer.sent))
	}
	mail := f.mailer.sent[0]
	if mail.to != alex.Email || mail.url != "https://wallet.example.org/proof/"+testRawToken || mail.validFor != SessionTTL {
		t.Errorf("mail = %+v", mail)
	}
}

func TestCreateRequestLinkNeverOutlivesSessionTTL(t *testing.T) {
	f := newFixture(true)
	f.ips.sessionExpires = time.Now().Add(time.Hour)
	f.send(t)
	if got := time.Until(f.requests.created[0].LinkExpiresAt); got > SessionTTL {
		t.Errorf("link lives %v, want at most %v", got, SessionTTL)
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

func TestStartMintsAFreshClaimOnTheSameSession(t *testing.T) {
	f := newFixture(true)
	f.send(t)
	for range 2 {
		res, err := f.svc.Start(context.Background(), testRawToken)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if res.Status != StatusInProgress || res.DeepLink != "vcmrtd://verify?handover=fresh" {
			t.Errorf("start = %+v", res)
		}
	}
	if len(f.ips.sessions) != 1 || f.ips.claims != 2 {
		t.Errorf("sessions = %d, claims = %d; want one session and a fresh claim per start", len(f.ips.sessions), f.ips.claims)
	}
}

func TestStartOnALapsedSessionIsAnExpiredLink(t *testing.T) {
	f := newFixture(true)
	f.send(t)
	f.requests.byToken[testRawToken].session.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := f.svc.Start(context.Background(), testRawToken); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("Start = %v, want ErrLinkNotFound", err)
	}
	if len(f.ips.sessions) != 1 {
		t.Error("a lapsed link created a new session")
	}
}

func TestLinkMarksStartedThenRecordsOutcomeOnce(t *testing.T) {
	f := newFixture(true)
	f.send(t)

	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusOpened}
	for range 2 {
		if _, err := f.svc.Link(context.Background(), testRawToken); err != nil {
			t.Fatalf("Link: %v", err)
		}
	}
	if f.requests.started != 1 {
		t.Errorf("started = %d, want once", f.requests.started)
	}

	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, AssuranceLevel: "high", EIDASLevel: "substantial"}
	for range 2 {
		link, err := f.svc.Link(context.Background(), testRawToken)
		if err != nil || link.Request.Status != StatusApproved {
			t.Fatalf("Link = %s, %v; want approved", link.Request.Status, err)
		}
	}
	if len(f.requests.outcomes) != 1 {
		t.Errorf("outcomes recorded = %v, want one", f.requests.outcomes)
	}
	if res, _ := f.svc.Start(context.Background(), testRawToken); res.Status != StatusApproved || res.DeepLink != "" {
		t.Errorf("start after approval = %+v, want settled with no link", res)
	}
}

func TestLinkEndsWhenIPSEndsTheSession(t *testing.T) {
	f := newFixture(true)
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusCancelled}
	link, err := f.svc.Link(context.Background(), testRawToken)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if f.requests.expired != 1 || link.Request.EffectiveStatus(time.Now().Add(time.Millisecond)) != StatusExpired {
		t.Errorf("expired = %d, status = %s; want the link ended", f.requests.expired, link.Request.EffectiveStatus(time.Now()))
	}
}

func TestLinkSurvivesIPSOutage(t *testing.T) {
	f := newFixture(true)
	f.send(t)
	f.ips.resultErr = errors.New("unreachable")
	link, err := f.svc.Link(context.Background(), testRawToken)
	if err != nil || link.Request.Status != StatusPending {
		t.Errorf("Link = %s, %v; want the last known status and no error", link.Request.Status, err)
	}
}

func TestEffectiveStatusExpiresUnfinishedOnly(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for status, want := range map[Status]Status{
		StatusPending: StatusExpired, StatusInProgress: StatusExpired,
		StatusApproved: StatusApproved, StatusNeedsReview: StatusNeedsReview,
	} {
		if got := (Request{Status: status, LinkExpiresAt: past}).EffectiveStatus(time.Now()); got != want {
			t.Errorf("%s past its link = %s, want %s", status, got, want)
		}
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
	if !strings.HasPrefix(ProofPath("a/b"), "/proof/") || strings.Contains(ProofPath("a/b"), "a/b") {
		t.Errorf("ProofPath does not escape the token: %q", ProofPath("a/b"))
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
