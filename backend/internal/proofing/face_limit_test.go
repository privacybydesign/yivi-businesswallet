package proofing

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The member face route counts each frame against its org, as the hosted
// one counts against its customer: each frame is a Regula call.
func TestMemberFaceFramesLimited(t *testing.T) {
	h := &Handler{memberFaceFrames: ratelimit.New(ratelimit.Limit{Burst: 1, Per: time.Minute})}
	served := 0
	route := respond.HandlerFunc(h.limitMemberFace(func(w http.ResponseWriter, _ *http.Request) error {
		served++
		w.WriteHeader(http.StatusOK)
		return nil
	}))
	frame := func(org uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/orgs/acme/identity-proofing/requests/r/yivi/face", nil)
		req = req.WithContext(organization.ContextWithOrg(req.Context(), organization.Organization{ID: org}))
		rec := httptest.NewRecorder()
		route.ServeHTTP(rec, req)
		return rec
	}
	acme, other := uuid.New(), uuid.New()
	if rec := frame(acme); rec.Code != http.StatusOK {
		t.Fatalf("first frame = %d", rec.Code)
	}
	if rec := frame(acme); rec.Code != http.StatusTooManyRequests || rec.Header().Get(headerRetryAfter) == "" {
		t.Errorf("frame past the budget = %d (Retry-After %q), want 429 with Retry-After", rec.Code, rec.Header().Get(headerRetryAfter))
	}
	if rec := frame(other); rec.Code != http.StatusOK {
		t.Errorf("another org's frame = %d, want its own budget", rec.Code)
	}
	if served != 2 {
		t.Errorf("served %d frames, want 2", served)
	}
}
