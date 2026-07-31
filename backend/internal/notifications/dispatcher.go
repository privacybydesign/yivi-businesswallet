package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultPollInterval is how often the dispatcher looks for queued events.
	// Notifications are not interactive, so a few seconds of latency is fine and
	// a short interval would poll an empty table all day.
	DefaultPollInterval = 15 * time.Second
	// DefaultNotifyTimeout bounds one channel's delivery attempt. Without it a
	// hung SMTP or webhook call would stall the whole pass.
	DefaultNotifyTimeout = 15 * time.Second
	// DefaultDeliverConcurrency is how many events a pass delivers at a time.
	// Delivery within one event is sequential over its channels, so a pass costs
	// at worst ceil(batch/concurrency) * channels * DefaultNotifyTimeout; without
	// the fan-out a batch of hanging channels would take that times concurrency,
	// during which nothing else drains.
	DefaultDeliverConcurrency = 8
)

// Channel is one delivery route for a notification (e-mail, Slack, MS Teams).
// Implementations live in their own packages and are registered on the Dispatcher
// at startup. Notify is called once per subscribed event and must respect the
// context deadline; returning an error is logged and the event moves on, it never
// affects another channel or the action that caused the event.
//
// Notify must treat e as read-only, Metadata included: the same Event value (and
// the same Metadata map) is handed to each channel subscribed to it, so mutating
// it changes what the next channel sees.
type Channel interface {
	ID() ChannelID
	Notify(ctx context.Context, e Event) error
}

// outboxClaimer is the drain side of the outbox, implemented by *Store.
type outboxClaimer interface {
	Claim(ctx context.Context, limit int) ([]Event, error)
}

// subscriptionReader reads an org's subscriptions, implemented by *Store.
type subscriptionReader interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
}

// Dispatcher drains the notification outbox and fans each event out to the
// channels its organization subscribed to.
type Dispatcher struct {
	outbox        outboxClaimer
	subscriptions subscriptionReader
	channels      map[ChannelID]Channel
	batch         int
	notifyTimeout time.Duration
	concurrency   int
}

// NewDispatcher builds a dispatcher over the outbox. Channels are registered
// separately (Register) because they are wired up per deployment; with none
// registered a pass still drains the queue and delivers nothing.
func NewDispatcher(outbox outboxClaimer, subscriptions subscriptionReader) *Dispatcher {
	return &Dispatcher{
		outbox:        outbox,
		subscriptions: subscriptions,
		channels:      map[ChannelID]Channel{},
		batch:         DefaultClaimBatch,
		notifyTimeout: DefaultNotifyTimeout,
		concurrency:   DefaultDeliverConcurrency,
	}
}

// Register adds a channel. Registering the same id twice replaces the earlier
// one; call it before Start.
func (d *Dispatcher) Register(c Channel) { d.channels[c.ID()] = c }

// Start drains the outbox every interval in the background until ctx is
// cancelled. It returns immediately.
func (d *Dispatcher) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := d.DispatchPending(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.ErrorContext(ctx, "notifications: dispatch pass failed",
						slog.String("error", err.Error()))
				}
			}
		}
	}()
}

// DispatchPending claims one batch of queued events and delivers each to the
// channels its org subscribed to. It returns the number of events claimed. Only a
// failure to read the queue is an error: a channel that fails is logged and the
// pass continues, so one broken channel cannot hold up the rest.
func (d *Dispatcher) DispatchPending(ctx context.Context) (int, error) {
	events, err := d.outbox.Claim(ctx, d.batch)
	if err != nil {
		return 0, err
	}
	settings := d.readSubscriptions(ctx, events)

	// Deliver up to concurrency events at a time. Events are independent of each
	// other, so a channel that sits on its deadline only delays the events sharing
	// its slot instead of the whole batch.
	sem := make(chan struct{}, d.concurrency)
	var wg sync.WaitGroup
	for _, e := range events {
		channels := settings[e.OrgID].ChannelsFor(e.Action)
		if len(channels) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d.deliver(ctx, e, channels)
		}()
	}
	wg.Wait()
	return len(events), nil
}

// readSubscriptions resolves the subscriptions of every org in the batch, one
// lookup per org: a burst of events usually belongs to the same organization. A
// read that fails is logged once and cached as the zero Settings, so the org's
// remaining events in this pass notify nobody rather than re-querying a database
// that is already in trouble — a full batch would otherwise cost one failed query
// and one ERROR line per event, every tick, per replica. The next pass retries.
func (d *Dispatcher) readSubscriptions(ctx context.Context, events []Event) map[uuid.UUID]Settings {
	settings := map[uuid.UUID]Settings{}
	for _, e := range events {
		if _, ok := settings[e.OrgID]; ok {
			continue
		}
		orgSettings, err := d.subscriptions.GetSettings(ctx, e.OrgID)
		if err != nil {
			slog.ErrorContext(ctx, "notifications: read subscriptions",
				slog.String("organizationId", e.OrgID.String()),
				slog.String("error", err.Error()))
			orgSettings = Settings{}
		}
		settings[e.OrgID] = orgSettings
	}
	return settings
}

// deliver sends one event to each subscribed channel in turn.
func (d *Dispatcher) deliver(ctx context.Context, e Event, channels []ChannelID) {
	for _, id := range channels {
		channel, ok := d.channels[id]
		if !ok {
			// Subscribed to a channel this deployment has not enabled. The
			// preference is kept, so it starts working once the channel is wired up.
			slog.WarnContext(ctx, "notifications: no handler for subscribed channel",
				slog.String("channel", string(id)),
				slog.String("action", e.Action),
				slog.String("organizationId", e.OrgID.String()))
			continue
		}
		if err := d.notify(ctx, channel, e); err != nil {
			slog.ErrorContext(ctx, "notifications: channel delivery failed",
				slog.String("channel", string(id)),
				slog.String("action", e.Action),
				slog.String("organizationId", e.OrgID.String()),
				slog.String("error", err.Error()))
		}
	}
}

// notify calls one channel under a deadline, turning a panic into an error. The
// dispatcher runs in its own goroutine, outside the HTTP recoverer middleware, so
// an unguarded panic in a channel would take the process down with it.
func (d *Dispatcher) notify(ctx context.Context, channel Channel, e Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("channel %s panicked: %v", channel.ID(), r)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, d.notifyTimeout)
	defer cancel()
	return channel.Notify(ctx, e)
}
