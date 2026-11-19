package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// flushRecorder is a ResponseRecorder whose writes a test can wait for.
type flushRecorder struct {
	*httptest.ResponseRecorder
	wrote chan string
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.wrote <- string(p)
	return len(p), nil
}

func (f *flushRecorder) Flush() {}

func TestServeSendsOnlySubscribedTopicsNamedByKind(t *testing.T) {
	hub := NewHub()
	org, other := uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan string, 8)}
	done := make(chan error, 1)
	go func() {
		done <- hub.Serve(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), Topic(Qerds, org), Topic(Signing, org))
	}()

	// Publish until the stream has subscribed.
	deadline := time.After(5 * time.Second)
	var frame string
	for frame == "" {
		hub.publish(Topic(Qerds, other))
		hub.publish(Topic(Reviews, org))
		hub.publish(Topic(Signing, org))
		select {
		case frame = <-rec.wrote:
			// The stream opens with a comment, before any event.
			if frame == openingComment {
				frame = ""
			}
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("no event reached the stream")
		}
	}
	if frame != "event: signing\ndata: \n\n" {
		t.Fatalf("frame = %q, want only the subscribed signing event", frame)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(hub.subs) != 0 {
		t.Fatalf("subscriptions left after the page went: %d", len(hub.subs))
	}
}

func TestStopEndsOpenStreams(t *testing.T) {
	hub := NewHub()
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan string, 8)}
	done := make(chan error, 1)
	go func() {
		done <- hub.Serve(rec, httptest.NewRequest(http.MethodGet, "/", nil), Topic(Qerds, uuid.New()))
	}()
	if first := <-rec.wrote; first != openingComment {
		t.Fatalf("first frame = %q, want the opening comment", first)
	}
	hub.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop left the stream open")
	}
}
