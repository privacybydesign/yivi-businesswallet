// Package events is the wallet's Observer: database triggers NOTIFY a topic
// whenever a row a page shows changes, one LISTEN per process hears them, and
// each open page gets the topics it subscribed to as Server-Sent Events. A
// page re-reads on an event, so nothing has to poll on a timer.
//
// An event carries no data, only that something of its kind changed: what the
// page then reads applies the reader's own access.
package events

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Channel is NOTIFYed with a topic by the notify_wallet_event triggers.
const Channel = "wallet_events"

// A Kind is what changed; it is the event's name on a stream. A topic is a
// kind and the id it changed for (Topic).
type Kind string

const (
	// Per organisation.
	Reviews  Kind = "reviews"
	Qerds    Kind = "qerds"
	Signing  Kind = "signing"
	Webhooks Kind = "webhooks"
	// An issued attestation or an incoming credential offer moved.
	Attestations Kind = "attestations"
	// A step of a verify session moved: its request, or its engine session.
	ProofingProgress Kind = "proofing-progress"
	// Per row, for a public page that follows one thing by its link.
	SigningRequest  Kind = "signing-request"
	ProofingRequest Kind = "proofing-request"
)

// OrgKinds are the kinds a member's page follows for its organisation.
var OrgKinds = []Kind{Reviews, Qerds, Signing, Webhooks, ProofingProgress, Attestations}

const topicSeparator = ":"

// Topic names kind for id, as the triggers send it.
func Topic(kind Kind, id uuid.UUID) string {
	return string(kind) + topicSeparator + id.String()
}

func kindOf(topic string) Kind {
	kind, _, _ := strings.Cut(topic, topicSeparator)
	return Kind(kind)
}

// keepAlive is how often an open stream sends a ping. It keeps a proxy between
// it and the page from closing the stream as idle, and it is how the page
// knows the stream still lives: a connection can die without either end being
// told (a proxy that keeps its side open, a laptop that slept), and a page
// that hears nothing for a few of these reconnects (STREAM_STALE_AFTER_MS in
// the frontend's api/event-stream.ts).
const keepAlive = 15 * time.Second

// pingEvent is the keep-alive: an event and not an SSE comment, which a page's
// EventSource never shows it.
const pingEvent = "event: ping\ndata: \n\n"

// openingComment is sent at once when a stream opens; the page reads nothing
// from it.
const openingComment = ":\n\n"

// subscriberBuffer holds the kinds not yet sent to a slow page: one slot per
// kind is enough, as a kind still unsent is the same event.
const subscriberBuffer = 16

// Hub hands each notified topic to the streams subscribed to it.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*subscriber]struct{}
	// stopping is closed by Stop, when the server shuts down: every stream
	// ends instead of holding the drain.
	stopping chan struct{}
	stopOnce sync.Once
}

type subscriber struct {
	kinds chan Kind
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[*subscriber]struct{}{}, stopping: make(chan struct{})}
}

// Run listens on Channel until ctx ends. Each time LISTEN holds, every
// subscriber is told all its kinds, for whatever changed before it did.
func (h *Hub) Run(ctx context.Context, pool *pgxpool.Pool) {
	database.Listen(ctx, pool, Channel, h.publishAll, h.publish)
}

// Stop ends every stream, and every later one at once: the server is shutting
// down (http.Server.RegisterOnShutdown). The page reconnects, at another
// replica.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() { close(h.stopping) })
}

// Subscribe returns a channel that gets the kind of each of topics as it
// changes, and the func that ends the subscription.
func (h *Hub) Subscribe(topics ...string) (<-chan Kind, func()) {
	sub := &subscriber{kinds: make(chan Kind, subscriberBuffer)}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, topic := range topics {
		if h.subs[topic] == nil {
			h.subs[topic] = map[*subscriber]struct{}{}
		}
		h.subs[topic][sub] = struct{}{}
	}
	return sub.kinds, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, topic := range topics {
			delete(h.subs[topic], sub)
			if len(h.subs[topic]) == 0 {
				delete(h.subs, topic)
			}
		}
	}
}

func (h *Hub) publish(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[topic] {
		sub.tell(kindOf(topic))
	}
}

func (h *Hub) publishAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic, subs := range h.subs {
		for sub := range subs {
			sub.tell(kindOf(topic))
		}
	}
}

// tell queues kind for the page. A full buffer means the page is far behind
// and already has an event of nearly every kind waiting; it re-reads then.
func (s *subscriber) tell(kind Kind) {
	select {
	case s.kinds <- kind:
	default:
	}
}

// Serve streams topics to the page as Server-Sent Events, each named by its
// kind and without data, until the page leaves or the server shuts down. The
// caller has already decided the reader may follow these topics.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, topics ...string) error {
	kinds, unsubscribe := h.Subscribe(topics...)
	defer unsubscribe()
	stream := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Nginx would hold the events back in its buffer otherwise.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// A first comment at once: some proxies hold the headers back until the
	// body starts, and the page only knows the stream is up once they arrive.
	if _, err := fmt.Fprint(w, openingComment); err != nil {
		return fmt.Errorf("events: open stream: %w", err)
	}
	if err := stream.Flush(); err != nil {
		return fmt.Errorf("events: open stream: %w", err)
	}
	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	for {
		var frame string
		select {
		case <-r.Context().Done():
			return nil
		case <-h.stopping:
			return nil
		case kind := <-kinds:
			frame = "event: " + string(kind) + "\ndata: \n\n"
		case <-ticker.C:
			frame = pingEvent
		}
		if _, err := fmt.Fprint(w, frame); err != nil {
			return nil //nolint:nilerr // the page is gone, which is how a stream ends
		}
		if stream.Flush() != nil {
			return nil
		}
	}
}
