package proofing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// fakePauses keeps pauses in memory, as the store would.
type fakePauses struct{ byOrg map[uuid.UUID]OrgPause }

func (f *fakePauses) Get(_ context.Context, orgID uuid.UUID) (OrgPause, error) {
	if p, ok := f.byOrg[orgID]; ok {
		return p, nil
	}
	return OrgPause{OrganizationID: orgID}, nil
}

func (f *fakePauses) List(context.Context) ([]OrgPause, error) {
	out := []OrgPause{}
	for _, p := range f.byOrg {
		if p.Paused() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakePauses) Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, state PauseState, _ *uuid.UUID) (OrgPause, error) {
	p, _ := f.Get(ctx, orgID)
	var at *time.Time
	if state == PauseOn {
		now := time.Now()
		at = &now
	}
	if level == PausePlatform {
		p.PlatformPausedAt = at
	} else {
		p.OrgPausedAt = at
	}
	f.byOrg[orgID] = p
	return p, nil
}

func withPauses(f fixture) *fakePauses {
	pauses := &fakePauses{byOrg: map[uuid.UUID]OrgPause{}}
	f.svc.pauses = pauses
	return pauses
}

func TestPauseLevelsLiftOwnOnly(t *testing.T) {
	f := newFixture()
	withPauses(f)
	ctx := context.Background()
	if err := f.svc.checkActive(ctx, testOrg.ID); err != nil {
		t.Fatalf("never paused: %v, want active", err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PausePlatform, PauseOn, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PauseOrganization, PauseOff, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.checkActive(ctx, testOrg.ID); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("platform pause, org switched on: %v, want ErrProofingPaused", err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PausePlatform, PauseOff, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PauseOrganization, PauseOn, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.checkActive(ctx, testOrg.ID); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("org switched off: %v, want ErrProofingPaused", err)
	}
	if err := f.svc.checkActive(ctx, uuid.New()); err != nil {
		t.Errorf("another org: %v, want active", err)
	}
}

func TestPausedOrgHostedLinkRefuses(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	withPauses(f)
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PausePlatform, PauseOn, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.HostedRequest(context.Background(), token); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("open while paused = %v, want ErrProofingPaused", err)
	}
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("start while paused = %v, want ErrProofingPaused", err)
	}
	if len(f.ips.sessions) != 0 {
		t.Errorf("a paused org made %d IPS sessions, want none", len(f.ips.sessions))
	}
	h := NewHandler(f.svc, nil, nil)
	header := http.Header{}
	h.PageHeaders(httptest.NewRequest(http.MethodGet, "/p/"+token, nil), header)
	if got := header.Get("Content-Security-Policy"); got != frameNone {
		t.Errorf("paused page CSP = %q, want %q", got, frameNone)
	}
}

func TestPausedOrgRoutesSayPaused(t *testing.T) {
	f := newFixture()
	withPauses(f)
	h := NewHandler(f.svc, nil, nil)
	reached := false
	route := h.active(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	call := func() int {
		r := httptest.NewRequest(http.MethodGet, "/orgs/acme/identity-proofing/flows", nil)
		r = r.WithContext(organization.ContextWithOrg(r.Context(), organization.Organization{ID: testOrg.ID}))
		rec := httptest.NewRecorder()
		route.ServeHTTP(rec, r)
		return rec.Code
	}
	if call(); !reached {
		t.Fatal("an active org's route was not reached")
	}
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PauseOrganization, PauseOn, nil); err != nil {
		t.Fatal(err)
	}
	reached = false
	if code := call(); code != http.StatusForbidden || reached {
		t.Errorf("paused org route = %d (reached %v), want 403 without reaching it", code, reached)
	}
}

// Pausing the org first rejects each review it has open, ORG_PAUSED with its
// reason: a paused org can decide nothing.
func TestOrgPauseRejectsOpenReviews(t *testing.T) {
	f := newFixture()
	withPauses(f)
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", req.Status)
	}
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PauseOrganization, PauseOn, nil); err != nil {
		t.Fatalf("SetProofingPaused: %v", err)
	}
	if len(f.ips.decisions) != 1 || f.ips.decisions[0].Approve || f.ips.decisions[0].ErrorCode != ErrorOrgPaused ||
		f.ips.decisions[0].Reason != orgPausedReason {
		t.Errorf("decisions = %+v; want one rejection with ORG_PAUSED and its reason", f.ips.decisions)
	}
}

// A customer with a session waiting for review cannot be paused until it is
// decided.
func TestCustomerPauseWaitsReviews(t *testing.T) {
	f := newFixture()
	f.sendForCustomer(t, "anna@example.org", "")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", req.Status)
	}
	if _, err := f.svc.SetCustomerStatus(context.Background(), testOrg.ID, initech.ID, CustomerPaused); !errors.Is(err, ErrCustomerHasOpenReviews) {
		t.Errorf("pause with an open review = %v, want %v", err, ErrCustomerHasOpenReviews)
	}
	if _, err := f.svc.SetCustomerStatus(context.Background(), testOrg.ID, initech.ID, CustomerActive); err != nil {
		t.Errorf("resume = %v, want it allowed", err)
	}
}

// A pause whose rejection of an open review fails is refused: nothing would
// be left to decide that review.
func TestPauseRefusedIfRejectFails(t *testing.T) {
	f := newFixture()
	pauses := withPauses(f)
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", req.Status)
	}
	f.ips.decideErr = errors.New("engine down")
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PauseOrganization, PauseOn, nil); err == nil {
		t.Fatal("pause with a review it could not reject went ahead")
	}
	if p, _ := pauses.Get(context.Background(), testOrg.ID); p.Paused() {
		t.Error("the org is paused with a review nobody can decide")
	}
}

// A session that reaches review while its org is paused is rejected at once,
// ORG_PAUSED: nobody could decide it.
func TestPausedOrgRejectsNewReview(t *testing.T) {
	f := newFixture()
	withPauses(f)
	f.send(t)
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PausePlatform, PauseOn, nil); err != nil {
		t.Fatalf("SetProofingPaused: %v", err)
	}
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	req := f.reconcile(t)
	if req.Status != StatusRejected || req.ErrorCode != ErrorOrgPaused {
		t.Errorf("status = %s %s, want rejected %s", req.Status, req.ErrorCode, ErrorOrgPaused)
	}
	if len(f.ips.decisions) != 1 || f.ips.decisions[0].Approve {
		t.Errorf("decisions = %+v, want one rejection", f.ips.decisions)
	}
}

// A review left open in a paused org, its rejection having failed, is
// rejected by the next purge run.
func TestSweepRejectsPausedReview(t *testing.T) {
	f := newFixture()
	withPauses(f)
	f.send(t)
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PauseOrganization, PauseOn, nil); err != nil {
		t.Fatalf("SetProofingPaused: %v", err)
	}
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	f.ips.decideErr = errors.New("engine down")
	if req := f.reconcile(t); req.Status != StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review while the engine is down", req.Status)
	}
	if _, err := f.svc.PurgeDue(context.Background()); err == nil {
		t.Error("PurgeDue with the engine down reported no failure")
	}
	f.ips.decideErr = nil
	if _, err := f.svc.PurgeDue(context.Background()); err != nil {
		t.Fatalf("PurgeDue: %v", err)
	}
	if got := f.requests.stored; got.Status != StatusRejected || got.ErrorCode != ErrorOrgPaused {
		t.Errorf("status = %s %s, want rejected %s", got.Status, got.ErrorCode, ErrorOrgPaused)
	}
}
