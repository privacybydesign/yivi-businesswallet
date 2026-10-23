package verification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
)

const (
	testAppBaseURL = "https://wallet.test"
	testTTL        = 15 * time.Minute
	testVCT        = "nl.nijmegen.apv.standplaatsvergunning"
)

type fakeStore struct {
	templates map[uuid.UUID]Template
	sessions  map[uuid.UUID]Session
	completed int
}

func newFakeStore() *fakeStore {
	return &fakeStore{templates: map[uuid.UUID]Template{}, sessions: map[uuid.UUID]Session{}}
}

func (f *fakeStore) GetTemplate(_ context.Context, orgID, id uuid.UUID) (Template, error) {
	t, ok := f.templates[id]
	if !ok || t.OrganizationID != orgID {
		return Template{}, ErrTemplateNotFound
	}
	return t, nil
}

func (f *fakeStore) CreateSession(_ context.Context, in NewSession) (Session, error) {
	s := Session{
		ID: uuid.New(), OrganizationID: in.Template.OrganizationID, TemplateID: &in.Template.ID,
		TemplateName: in.Template.Name, VCT: in.Template.VCT, TransactionID: in.TransactionID,
		WalletLink: in.WalletLink, Status: StatusPending, StartedByUserID: &in.StartedBy,
		ExpiresAt: in.ExpiresAt, CreatedAt: time.Now(),
	}
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeStore) GetSession(_ context.Context, orgID, id uuid.UUID) (Session, error) {
	s, ok := f.sessions[id]
	if !ok || s.OrganizationID != orgID {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeStore) ListSessions(_ context.Context, orgID uuid.UUID) ([]Session, error) {
	out := []Session{}
	for _, s := range f.sessions {
		if s.OrganizationID == orgID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) CompleteSession(_ context.Context, orgID, id uuid.UUID, res Result) (Session, error) {
	s, ok := f.sessions[id]
	if !ok || s.OrganizationID != orgID {
		return Session{}, ErrSessionNotFound
	}
	if s.Status != StatusPending {
		return s, nil
	}
	f.completed++
	now := time.Now()
	valid := res.Valid
	s.Status, s.Claims, s.Checks, s.Valid, s.CompletedAt = StatusCompleted, res.Claims, res.Checks, &valid, &now
	f.sessions[id] = s
	return s, nil
}

type fakeVerifier struct {
	started  []openid4vpverifier.Query
	startErr error
	result   openid4vpverifier.Presentation
	pending  bool
}

func (f *fakeVerifier) StartQuery(_ context.Context, q openid4vpverifier.Query) (openid4vpverifier.Session, error) {
	f.started = append(f.started, q)
	if f.startErr != nil {
		return openid4vpverifier.Session{}, f.startErr
	}
	return openid4vpverifier.Session{TransactionID: "tx-1", WalletLink: "openid4vp://?client_id=x509_san_dns%3Averifier.test&request_uri=https%3A%2F%2Fverifier.test%2Freq%2F1"}, nil
}

func (f *fakeVerifier) Result(_ context.Context, _ string) (openid4vpverifier.Presentation, error) {
	if f.pending {
		return openid4vpverifier.Presentation{}, openid4vpverifier.ErrPending
	}
	return f.result, nil
}

type fakeLedger struct {
	entry attestation.Issued
	err   error
	asked map[string]string
}

func (f *fakeLedger) FindIssuedByClaims(_ context.Context, _ uuid.UUID, _ string, claims map[string]string) (attestation.Issued, error) {
	f.asked = claims
	return f.entry, f.err
}

type harness struct {
	svc      *Service
	store    *fakeStore
	verifier *fakeVerifier
	ledger   *fakeLedger
	org      uuid.UUID
	template Template
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := newFakeStore()
	org := uuid.New()
	tpl := Template{ID: uuid.New(), OrganizationID: org, Name: "APV", VCT: testVCT, Claims: []string{"vergunningnummer", "markt"}}
	store.templates[tpl.ID] = tpl
	v := &fakeVerifier{}
	l := &fakeLedger{err: attestation.ErrIssuedNotFound}
	svc := NewService(store, store, v, l, testAppBaseURL, testTTL)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return &harness{svc: svc, store: store, verifier: v, ledger: l, org: org, template: tpl, now: now}
}

func presentation(claims map[string]string, exp time.Time) openid4vpverifier.Presentation {
	p := openid4vpverifier.Presentation{
		Claims:       claims,
		ByCredential: map[string]map[string]string{openid4vpverifier.QueryCredentialID: claims},
	}
	if !exp.IsZero() {
		p.ExpiresAt = map[string]time.Time{openid4vpverifier.QueryCredentialID: exp}
	}
	return p
}

func checkByName(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q missing from %+v", name, checks)
	return Check{}
}

func TestStartMintsRequestFromTemplateAndReturnsBothLinks(t *testing.T) {
	h := newHarness(t)
	member := uuid.New()

	v, err := h.svc.Start(context.Background(), h.org, h.template.ID, member)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(h.verifier.started) != 1 || h.verifier.started[0].VCT != testVCT || len(h.verifier.started[0].Claims) != 2 {
		t.Errorf("verifier asked %+v, want the template's vct and claims", h.verifier.started)
	}
	if v.Status != StatusPending || v.TemplateName != "APV" || v.StartedByUserID == nil || *v.StartedByUserID != member {
		t.Errorf("view = %+v", v.Session)
	}
	if v.WalletLink == "" {
		t.Error("wallet link must be returned while pending")
	}
	want := "https://wallet.test/openid4vp?client_id=x509_san_dns%3Averifier.test&request_uri=https%3A%2F%2Fverifier.test%2Freq%2F1"
	if v.BrowserLink != want {
		t.Errorf("browser link = %q, want %q", v.BrowserLink, want)
	}
	if !v.ExpiresAt.Equal(h.now.Add(testTTL)) {
		t.Errorf("expires at = %v, want now+ttl", v.ExpiresAt)
	}
}

func TestStartUnknownTemplateIsNotFound(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Start(context.Background(), h.org, uuid.New(), uuid.New()); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("err = %v, want ErrTemplateNotFound", err)
	}
	if len(h.verifier.started) != 0 {
		t.Error("verifier must not be called for an unknown template")
	}
}

func TestStartTemplateOfAnotherOrgIsNotFound(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Start(context.Background(), uuid.New(), h.template.ID, uuid.New()); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("err = %v, want ErrTemplateNotFound", err)
	}
}

func TestStartVerifierFailureIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.verifier.startErr = errors.New("boom")
	if _, err := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New()); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("err = %v, want ErrVerifierUnavailable", err)
	}
	if len(h.store.sessions) != 0 {
		t.Error("no session may be recorded when the verifier refused the request")
	}
}

func TestGetStaysPendingWhileHolderHasNotAnswered(t *testing.T) {
	h := newHarness(t)
	h.verifier.pending = true
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Status != StatusPending || v.BrowserLink == "" || v.Valid != nil {
		t.Errorf("view = %+v", v)
	}
}

func TestGetGradesAValidPermit(t *testing.T) {
	h := newHarness(t)
	claims := map[string]string{"vergunningnummer": "APV-2026-01834", "markt": "Grote Markt Nijmegen"}
	h.verifier.result = presentation(claims, h.now.Add(24*time.Hour))
	h.ledger.entry, h.ledger.err = attestation.Issued{Status: attestation.StatusClaimed}, nil
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Status != StatusCompleted || v.Valid == nil || !*v.Valid {
		t.Fatalf("view = %+v, want completed and valid", v.Session)
	}
	if v.Claims["vergunningnummer"] != "APV-2026-01834" {
		t.Errorf("claims = %v", v.Claims)
	}
	if h.ledger.asked["markt"] != "Grote Markt Nijmegen" {
		t.Errorf("ledger looked up with %v, want the disclosed claims", h.ledger.asked)
	}
	for _, name := range []string{CheckVerified, CheckNotExpired, CheckIssuedHere, CheckNotRevoked} {
		if c := checkByName(t, v.Checks, name); !c.Passed {
			t.Errorf("check %s failed: %+v", name, c)
		}
	}
	if v.WalletLink != "" || v.BrowserLink != "" {
		t.Error("links must not be returned once completed")
	}

	// A second read returns the stored result without grading again.
	again, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if h.store.completed != 1 || again.Status != StatusCompleted {
		t.Errorf("completed %d times, status %s", h.store.completed, again.Status)
	}
}

func TestGetFailsARevokedPermit(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = presentation(map[string]string{"vergunningnummer": "APV-1"}, time.Time{})
	h.ledger.entry, h.ledger.err = attestation.Issued{Status: attestation.StatusRevoked}, nil
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Valid == nil || *v.Valid {
		t.Fatalf("want invalid, got %+v", v.Session)
	}
	if c := checkByName(t, v.Checks, CheckNotRevoked); c.Passed || c.Detail != attestation.StatusRevoked {
		t.Errorf("not_revoked = %+v", c)
	}
	if c := checkByName(t, v.Checks, CheckIssuedHere); !c.Passed {
		t.Errorf("issued_here = %+v, want passed", c)
	}
}

func TestGetFailsAPermitUnknownToTheLedger(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = presentation(map[string]string{"vergunningnummer": "APV-1"}, time.Time{})
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Valid == nil || *v.Valid {
		t.Fatalf("want invalid, got %+v", v.Session)
	}
	if c := checkByName(t, v.Checks, CheckIssuedHere); c.Passed {
		t.Errorf("issued_here = %+v, want failed", c)
	}
	if c := checkByName(t, v.Checks, CheckNotRevoked); c.Passed || c.Detail != "no_ledger_entry" {
		t.Errorf("not_revoked = %+v", c)
	}
	if c := checkByName(t, v.Checks, CheckNotExpired); !c.Passed {
		t.Errorf("not_expired without any known expiry = %+v, want passed", c)
	}
}

func TestGetFailsAnExpiredCredential(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = presentation(map[string]string{"vergunningnummer": "APV-1"}, h.now.Add(-time.Hour))
	h.ledger.entry, h.ledger.err = attestation.Issued{Status: attestation.StatusClaimed}, nil
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Valid == nil || *v.Valid {
		t.Fatalf("want invalid, got %+v", v.Session)
	}
	if c := checkByName(t, v.Checks, CheckNotExpired); c.Passed || c.Detail == "" {
		t.Errorf("not_expired = %+v, want failed with the expiry", c)
	}
}

func TestGetUsesLedgerExpiryWhenCredentialCarriesNone(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = presentation(map[string]string{"vergunningnummer": "APV-1"}, time.Time{})
	past := h.now.Add(-time.Hour)
	h.ledger.entry, h.ledger.err = attestation.Issued{Status: attestation.StatusClaimed, ExpiresAt: &past}, nil
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c := checkByName(t, v.Checks, CheckNotExpired); c.Passed {
		t.Errorf("not_expired = %+v, want failed from the ledger expiry", c)
	}
}

func TestGetFailsVerifiedWhenNothingWasDisclosed(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = openid4vpverifier.Presentation{}
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Status != StatusCompleted || v.Valid == nil || *v.Valid {
		t.Fatalf("want completed and invalid, got %+v", v.Session)
	}
	if len(v.Checks) != 1 || v.Checks[0].Name != CheckVerified || v.Checks[0].Passed {
		t.Errorf("checks = %+v, want only a failed verified check", v.Checks)
	}
}

func TestGetReportsExpiredSessionWithoutPollingTheVerifier(t *testing.T) {
	h := newHarness(t)
	h.verifier.result = presentation(map[string]string{"vergunningnummer": "APV-1"}, time.Time{})
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())
	h.svc.now = func() time.Time { return h.now.Add(testTTL) }

	v, err := h.svc.Get(context.Background(), h.org, started.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Status != StatusExpired || v.BrowserLink != "" || h.store.completed != 0 {
		t.Errorf("view = %+v (completed %d)", v, h.store.completed)
	}
}

func TestGetOtherOrgSessionIsNotFound(t *testing.T) {
	h := newHarness(t)
	started, _ := h.svc.Start(context.Background(), h.org, h.template.ID, uuid.New())
	if _, err := h.svc.Get(context.Background(), uuid.New(), started.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestBrowserLinkKeepsTheInvocationQuery(t *testing.T) {
	got := browserLink("https://wallet.test/", "openid4vp://?client_id=a&request_uri=b")
	if got != "https://wallet.test/openid4vp?client_id=a&request_uri=b" {
		t.Errorf("browser link = %q", got)
	}
}
