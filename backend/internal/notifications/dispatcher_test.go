package notifications

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubOutbox hands out one batch and then reports the queue empty.
type stubOutbox struct {
	events []Event
	err    error
	calls  int
}

func (o *stubOutbox) Claim(context.Context, int) ([]Event, error) {
	o.calls++
	if o.err != nil {
		return nil, o.err
	}
	if o.calls > 1 {
		return nil, nil
	}
	return o.events, nil
}

// stubSubscriptions answers with a fixed document per org.
type stubSubscriptions struct {
	byOrg   map[uuid.UUID]Settings
	err     error
	lookups int
}

func (s *stubSubscriptions) GetSettings(_ context.Context, orgID uuid.UUID) (Settings, error) {
	s.lookups++
	if s.err != nil {
		return Settings{}, s.err
	}
	return s.byOrg[orgID], nil
}

// stubChannel records what it was handed and can fail or panic on demand. A pass
// delivers several events at a time, so the recording is mutex-guarded.
type stubChannel struct {
	id     ChannelID
	err    error
	panics bool

	mu       sync.Mutex
	got      []Event
	deadline bool
}

func (c *stubChannel) ID() ChannelID { return c.id }

func (c *stubChannel) Notify(ctx context.Context, e Event) error {
	c.mu.Lock()
	c.got = append(c.got, e)
	if _, ok := ctx.Deadline(); ok {
		c.deadline = true
	}
	c.mu.Unlock()
	if c.panics {
		panic("channel exploded")
	}
	return c.err
}

// received is what the channel was handed, in no particular order.
func (c *stubChannel) received() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.got...)
}

func (c *stubChannel) sawDeadline() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func event(orgID uuid.UUID, action string) Event {
	return Event{ID: uuid.New(), OrgID: orgID, Action: action, OccurredAt: time.Now()}
}

func subscribed(orgID uuid.UUID, action string, channels ...ChannelID) *stubSubscriptions {
	return &stubSubscriptions{byOrg: map[uuid.UUID]Settings{
		orgID: {Configured: true, Subscriptions: map[string][]ChannelID{action: channels}},
	}}
}

func TestDispatchDeliversOnlyToSubscribedChannels(t *testing.T) {
	orgID := uuid.New()
	e := event(orgID, "membership.invited")
	mail := &stubChannel{id: ChannelEmail}
	slack := &stubChannel{id: ChannelSlack}

	d := NewDispatcher(&stubOutbox{events: []Event{e}}, subscribed(orgID, "membership.invited", ChannelEmail))
	d.Register(mail)
	d.Register(slack)

	n, err := d.DispatchPending(context.Background())
	if err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}
	if n != 1 {
		t.Errorf("dispatched %d events, want 1", n)
	}
	if got := mail.received(); len(got) != 1 || got[0].ID != e.ID {
		t.Errorf("email channel got %v, want the claimed event", got)
	}
	if got := slack.received(); len(got) != 0 {
		t.Errorf("slack channel got %v, want nothing (not subscribed)", got)
	}
	if !mail.sawDeadline() {
		t.Error("the channel was called without a deadline")
	}
}

func TestDispatchSkipsAnUnsubscribedEvent(t *testing.T) {
	orgID := uuid.New()
	mail := &stubChannel{id: ChannelEmail}

	d := NewDispatcher(&stubOutbox{events: []Event{event(orgID, "wallet.revoked")}},
		subscribed(orgID, "membership.invited", ChannelEmail))
	d.Register(mail)

	if _, err := d.DispatchPending(context.Background()); err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}
	if got := mail.received(); len(got) != 0 {
		t.Errorf("email channel got %v, want nothing for an unsubscribed event", got)
	}
}

// A failing or panicking channel must not stop the channels behind it: that is
// the "dispatch failures on one channel must not block others" rule.
func TestDispatchContinuesAfterAChannelFails(t *testing.T) {
	orgID := uuid.New()
	cases := map[string]*stubChannel{
		"returns an error": {id: ChannelEmail, err: errors.New("smtp down")},
		"panics":           {id: ChannelEmail, panics: true},
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			teams := &stubChannel{id: ChannelTeams}
			d := NewDispatcher(&stubOutbox{events: []Event{event(orgID, "membership.invited")}},
				subscribed(orgID, "membership.invited", ChannelEmail, ChannelTeams))
			d.Register(broken)
			d.Register(teams)

			n, err := d.DispatchPending(context.Background())
			if err != nil {
				t.Fatalf("DispatchPending: %v", err)
			}
			if n != 1 {
				t.Errorf("dispatched %d events, want 1", n)
			}
			if got := teams.received(); len(got) != 1 {
				t.Errorf("teams channel got %v, want the event despite the broken channel", got)
			}
		})
	}
}

func TestDispatchSkipsAChannelThatIsNotRegistered(t *testing.T) {
	orgID := uuid.New()
	mail := &stubChannel{id: ChannelEmail}

	// Subscribed to Slack, which this deployment has not wired up.
	d := NewDispatcher(&stubOutbox{events: []Event{event(orgID, "membership.invited")}},
		subscribed(orgID, "membership.invited", ChannelEmail, ChannelSlack))
	d.Register(mail)

	n, err := d.DispatchPending(context.Background())
	if err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}
	if got := mail.received(); n != 1 || len(got) != 1 {
		t.Errorf("dispatched %d events with %d e-mails, want 1 and 1", n, len(got))
	}
}

func TestDispatchKeepsGoingWhenOneOrgsSubscriptionsCannotBeRead(t *testing.T) {
	orgID := uuid.New()
	subs := &stubSubscriptions{err: errors.New("database down")}

	d := NewDispatcher(&stubOutbox{events: []Event{event(orgID, "membership.invited")}}, subs)
	d.Register(&stubChannel{id: ChannelEmail})

	n, err := d.DispatchPending(context.Background())
	if err != nil {
		t.Fatalf("DispatchPending = %v, want the pass to survive a settings read failure", err)
	}
	if n != 1 {
		t.Errorf("dispatched %d events, want the claimed one counted", n)
	}
}

func TestDispatchReadsSubscriptionsOncePerOrgPerPass(t *testing.T) {
	orgID := uuid.New()
	subs := subscribed(orgID, "membership.invited", ChannelEmail)
	events := []Event{
		event(orgID, "membership.invited"),
		event(orgID, "membership.invited"),
		event(orgID, "membership.revoked"),
	}

	d := NewDispatcher(&stubOutbox{events: events}, subs)
	d.Register(&stubChannel{id: ChannelEmail})

	if _, err := d.DispatchPending(context.Background()); err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}
	if subs.lookups != 1 {
		t.Errorf("read subscriptions %d times, want 1 for three events of one org", subs.lookups)
	}
}

// The once-per-org-per-pass budget has to hold when the read fails too, otherwise
// a database blip costs one failed query and one ERROR line per queued event,
// every tick, per replica — against a database that is already in trouble.
func TestDispatchCachesAFailedSubscriptionsReadForThePass(t *testing.T) {
	orgID := uuid.New()
	subs := &stubSubscriptions{err: errors.New("database down")}
	events := []Event{
		event(orgID, "membership.invited"),
		event(orgID, "membership.invited"),
		event(orgID, "membership.revoked"),
	}

	mail := &stubChannel{id: ChannelEmail}
	d := NewDispatcher(&stubOutbox{events: events}, subs)
	d.Register(mail)

	if _, err := d.DispatchPending(context.Background()); err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}
	if subs.lookups != 1 {
		t.Errorf("read subscriptions %d times, want 1 failed read for three events of one org", subs.lookups)
	}
	if got := mail.received(); len(got) != 0 {
		t.Errorf("email channel got %v, want nothing when the subscriptions are unknown", got)
	}
}

// blockingChannel reports every arrival and returns only once released, so a test
// can observe how many deliveries are in flight without a timing assumption.
type blockingChannel struct {
	id      ChannelID
	arrived chan struct{}
	release chan struct{}
}

func (c *blockingChannel) ID() ChannelID { return c.id }

func (c *blockingChannel) Notify(context.Context, Event) error {
	c.arrived <- struct{}{}
	<-c.release
	return nil
}

// A pass delivers several events at a time, so a channel sitting on its deadline
// delays the events sharing its slot instead of the whole batch.
func TestDispatchDeliversEventsConcurrently(t *testing.T) {
	orgID := uuid.New()
	events := []Event{event(orgID, "membership.invited"), event(orgID, "membership.invited")}
	channel := &blockingChannel{
		id:      ChannelEmail,
		arrived: make(chan struct{}, len(events)),
		release: make(chan struct{}),
	}

	d := NewDispatcher(&stubOutbox{events: events}, subscribed(orgID, "membership.invited", ChannelEmail))
	d.Register(channel)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := d.DispatchPending(context.Background()); err != nil {
			t.Errorf("DispatchPending: %v", err)
		}
	}()

	for i := range events {
		select {
		case <-channel.arrived:
		case <-time.After(10 * time.Second):
			close(channel.release)
			t.Fatalf("%d of %d events were in delivery, want both at once", i, len(events))
		}
	}
	close(channel.release)
	<-done
}

func TestDispatchReportsAFailureToReadTheQueue(t *testing.T) {
	d := NewDispatcher(&stubOutbox{err: errors.New("queue down")}, &stubSubscriptions{})
	if _, err := d.DispatchPending(context.Background()); err == nil {
		t.Fatal("DispatchPending succeeded, want the claim error")
	}
}
