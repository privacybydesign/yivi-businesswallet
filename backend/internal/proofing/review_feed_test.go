package proofing

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func TestReviewFeedTellsOnlyTheOrg(t *testing.T) {
	feed := NewReviewFeed()
	acme, beta := uuid.New(), uuid.New()
	acmeChanged, stopAcme := feed.subscribe(acme)
	betaChanged, stopBeta := feed.subscribe(beta)
	defer stopBeta()

	feed.publish(acme)
	feed.publish(acme)
	if !told(acmeChanged) {
		t.Error("acme's subscriber was not told")
	}
	if told(acmeChanged) {
		t.Error("two changes before a read are told twice; want once")
	}
	if told(betaChanged) {
		t.Error("beta's subscriber was told of acme's change")
	}

	feed.publishAll()
	if !told(acmeChanged) || !told(betaChanged) {
		t.Error("publishAll did not tell every subscriber")
	}

	stopAcme()
	feed.publish(acme)
	if told(acmeChanged) {
		t.Error("an ended subscription is still told")
	}
	if _, ok := feed.subs[acme]; ok {
		t.Error("an org without subscribers is still held")
	}
}

func TestReviewEventsStreamsAChange(t *testing.T) {
	h := &Handler{reviews: NewReviewFeed()}
	org := organization.Organization{ID: uuid.New()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond.HandlerFunc(h.reviewEvents).ServeHTTP(w, r.WithContext(organization.ContextWithOrg(r.Context(), org)))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL) //nolint:noctx // the test server is local
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	// The headers arrive once the stream subscribed.
	h.reviews.publish(org.ID)
	lines := bufio.NewReader(resp.Body)
	line, err := lines.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "event: reviews" {
		t.Fatalf("first line = %q, %v; want the reviews event", line, err)
	}

	h.reviews.Stop()
	if _, err := io.ReadAll(lines); err != nil {
		t.Errorf("stream after Stop: %v; want it to end", err)
	}
}

func told(changed <-chan struct{}) bool {
	select {
	case <-changed:
		return true
	default:
		return false
	}
}
