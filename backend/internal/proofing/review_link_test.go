package proofing

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeReviewLinks holds one live link, by its token.
type fakeReviewLinks struct {
	token string
	link  ReviewLink
}

func (f fakeReviewLinks) Create(context.Context, Request, uuid.UUID, string, []byte, time.Time) error {
	return nil
}

func (f fakeReviewLinks) Get(_ context.Context, hash []byte, _ time.Time) (ReviewLink, error) {
	want := sha256.Sum256([]byte(f.token))
	if string(hash) != string(want[:]) {
		return ReviewLink{}, ErrReviewLinkNotFound
	}
	return f.link, nil
}

// A review link's page may be framed only by its org's allowed origins, and
// by none for an unknown link.
func TestReviewPageFrameOrigins(t *testing.T) {
	f := newFixture()
	f.svc.reviewLinks = fakeReviewLinks{token: "tok", link: ReviewLink{OrganizationID: testOrg.ID, KeyPrefix: "yp_live_ab12"}}
	h := NewHandler(f.svc, nil, nil)
	csp := func(path string) string {
		header := http.Header{}
		h.PageHeaders(httptest.NewRequest(http.MethodGet, path, nil), header)
		return header.Get(headerCSP)
	}
	if got := csp("/r/tok"); got != frameNone {
		t.Errorf("no origins: CSP = %q, want %q", got, frameNone)
	}
	if _, err := saveOrigins(f.svc, []string{"https://backoffice.acme.example"}); err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	if got, want := csp("/r/tok"), "frame-ancestors https://backoffice.acme.example"; got != want {
		t.Errorf("with origins: CSP = %q, want %q", got, want)
	}
	if got := csp("/r/unknown"); got != frameNone {
		t.Errorf("unknown link: CSP = %q, want %q", got, frameNone)
	}
	for _, path := range []string{"/r/", "/r/tok/x"} {
		if got := csp(path); got != "" {
			t.Errorf("%s: CSP = %q, want none set", path, got)
		}
	}
}

func TestAPIReviewer(t *testing.T) {
	for raw, ok := range map[string]bool{
		"Jan Jansen": true, "  Petra  ": true, "": false, "   ": false,
		string(make([]rune, maxReviewerNameLength+1)): false, "Jan\x00": false,
	} {
		if _, err := apiReviewer(raw); (err == nil) != ok {
			t.Errorf("apiReviewer(%q) = %v, want ok %v", raw, err, ok)
		}
	}
}
