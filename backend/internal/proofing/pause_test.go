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

func (f *fakePauses) Set(ctx context.Context, orgID uuid.UUID, level PauseLevel, paused bool) (OrgPause, error) {
	p, _ := f.Get(ctx, orgID)
	var at *time.Time
	if paused {
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

func TestEitherPauseStopsAnOrgsProofingAndOnlyItsOwnLevelLiftsIt(t *testing.T) {
	f := newFixture(true)
	withPauses(f)
	ctx := context.Background()
	if err := f.svc.checkActive(ctx, testOrg.ID); err != nil {
		t.Fatalf("never paused: %v, want active", err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PausePlatform, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PauseOrganization, false); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.checkActive(ctx, testOrg.ID); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("platform pause, org switched on: %v, want ErrProofingPaused", err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PausePlatform, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetProofingPaused(ctx, testOrg.ID, PauseOrganization, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.checkActive(ctx, testOrg.ID); !errors.Is(err, ErrProofingPaused) {
		t.Errorf("org switched off: %v, want ErrProofingPaused", err)
	}
	if err := f.svc.checkActive(ctx, uuid.New()); err != nil {
		t.Errorf("another org: %v, want active", err)
	}
}

func TestAPausedOrgsHostedLinkRefusesEverything(t *testing.T) {
	f := newFixture(true)
	_, token := f.sendHosted(t)
	withPauses(f)
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PausePlatform, true); err != nil {
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

func TestAPausedOrgsRoutesAnswerProofingPaused(t *testing.T) {
	f := newFixture(true)
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
	if _, err := f.svc.SetProofingPaused(context.Background(), testOrg.ID, PauseOrganization, true); err != nil {
		t.Fatal(err)
	}
	reached = false
	if code := call(); code != http.StatusForbidden || reached {
		t.Errorf("paused org route = %d (reached %v), want 403 without reaching it", code, reached)
	}
}
