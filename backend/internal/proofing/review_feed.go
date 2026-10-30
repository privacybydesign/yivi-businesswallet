package proofing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// ReviewsChannel is NOTIFYed with an org's id whenever one of its requests
// goes into or out of review, its address review moves, or it is purged: a
// trigger on identity_proofing_requests sends it, whoever writes the row.
const ReviewsChannel = "identity_proofing_reviews"

// reviewEventsKeepAlive is how often an open stream sends a comment, so a
// proxy between it and the page does not close it as idle.
const reviewEventsKeepAlive = 25 * time.Second

// reviewsChangedEvent is the one event a stream sends: what the org's
// reviewers see changed. It carries no data: the page re-reads its lists,
// which apply the reader's own scope.
const reviewsChangedEvent = "event: reviews\ndata: \n\n"

// keepAliveComment is an SSE comment: the page reads nothing from it.
const keepAliveComment = ":\n\n"

// ReviewFeed tells the open Reviews pages of an org that what its reviewers
// see changed, so they re-read it then: one LISTEN per process, handed to each
// subscriber of the org named.
type ReviewFeed struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan struct{}]struct{}
	// stopping is closed by Stop, when the server shuts down: every stream
	// ends instead of holding the drain.
	stopping chan struct{}
	stopOnce sync.Once
}

func NewReviewFeed() *ReviewFeed {
	return &ReviewFeed{subs: map[uuid.UUID]map[chan struct{}]struct{}{}, stopping: make(chan struct{})}
}

// Run listens on ReviewsChannel until ctx ends. Each time LISTEN holds, every
// subscriber is told, for whatever changed before it did.
func (f *ReviewFeed) Run(ctx context.Context, pool *pgxpool.Pool) {
	database.Listen(ctx, pool, ReviewsChannel, f.publishAll, func(payload string) {
		orgID, err := uuid.Parse(payload)
		if err != nil {
			slog.WarnContext(ctx, "review feed: notification without an org id", slog.String("payload", payload))
			return
		}
		f.publish(orgID)
	})
}

// Stop ends every stream, and every later one at once: the server is shutting
// down (http.Server.RegisterOnShutdown). The page reconnects, at another
// replica.
func (f *ReviewFeed) Stop() {
	f.stopOnce.Do(func() { close(f.stopping) })
}

// subscribe returns a channel told when orgID's reviews change, and the func
// that ends the subscription. A change while one is still unread is the same
// change: the page re-reads everything either way.
func (f *ReviewFeed) subscribe(orgID uuid.UUID) (<-chan struct{}, func()) {
	changed := make(chan struct{}, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs[orgID] == nil {
		f.subs[orgID] = map[chan struct{}]struct{}{}
	}
	f.subs[orgID][changed] = struct{}{}
	return changed, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs[orgID], changed)
		if len(f.subs[orgID]) == 0 {
			delete(f.subs, orgID)
		}
	}
}

func (f *ReviewFeed) publish(orgID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for changed := range f.subs[orgID] {
		tell(changed)
	}
}

func (f *ReviewFeed) publishAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, subs := range f.subs {
		for changed := range subs {
			tell(changed)
		}
	}
}

func tell(changed chan struct{}) {
	select {
	case changed <- struct{}{}:
	default:
	}
}

// reviewEvents streams reviewsChangedEvent to a reviewer's page each time the
// org's reviews change, so its count and lists follow at once (Server-Sent
// Events). It ends when the page leaves or the server shuts down.
func (h *Handler) reviewEvents(w http.ResponseWriter, r *http.Request) error {
	changed, unsubscribe := h.reviews.subscribe(organization.OrgFromContext(r.Context()).ID)
	defer unsubscribe()
	stream := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Nginx would hold the events back in its buffer otherwise.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := stream.Flush(); err != nil {
		return fmt.Errorf("proofing: open review events: %w", err)
	}
	keepAlive := time.NewTicker(reviewEventsKeepAlive)
	defer keepAlive.Stop()
	for {
		var frame string
		select {
		case <-r.Context().Done():
			return nil
		case <-h.reviews.stopping:
			return nil
		case <-changed:
			frame = reviewsChangedEvent
		case <-keepAlive.C:
			frame = keepAliveComment
		}
		if !sendFrame(w, stream, frame) {
			return nil
		}
	}
}

// sendFrame writes frame to the page at once; false once the page is gone,
// which is how a stream that outlives its page ends.
func sendFrame(w http.ResponseWriter, stream *http.ResponseController, frame string) bool {
	if _, err := fmt.Fprint(w, frame); err != nil {
		return false
	}
	return stream.Flush() == nil
}
