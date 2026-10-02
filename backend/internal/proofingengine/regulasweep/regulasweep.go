// Package regulasweep deletes a session's Regula liveness transactions once
// the session is over, whatever became of them. The app runs liveness
// straight against the Face API, tagged with the session's Regula tag, and
// sends the server only the transaction it submits as the face step, which
// the server deletes right after the match. Every other attempt (a failed
// liveness, one abandoned before it was sent, a delete that failed) would
// keep its portrait and video in Regula's store: the design keeps no face
// data but the score and the verdict. So the server queues each tag it hands
// out, due after the session's end, and a sweep deletes every transaction
// with that tag (DELETE /api/v2/liveness?tag=), retrying until it succeeds.
//
// Postgres-only, like internal/idempotency: the queue must outlive a restart
// and be shared by every instance. Without a database the caller leaves the
// queue nil and nothing is swept; the Face API's own HouseKeeper
// (houseKeeper.liveness) is the backstop there.
package regulasweep

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Grace is how long after a session's end its tag is swept: past any
// submission still in flight when the session ended.
const Grace = 15 * time.Minute

// Batch bounds the tags one sweep deletes.
const Batch = 100

// Backoff after a failed delete: doubling from MinBackoff up to MaxBackoff.
const (
	MinBackoff = time.Minute
	MaxBackoff = time.Hour
)

// Entry is one queued tag.
type Entry struct {
	Tag      string
	DueAt    time.Time
	Attempts int
}

// Queue holds the tags to sweep.
type Queue interface {
	// Add queues tag, due at dueAt; a tag already queued keeps the later of
	// the two due times, so a session that runs longer is not swept early.
	Add(ctx context.Context, tag string, dueAt time.Time) error
	// Due returns up to limit tags due at now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Entry, error)
	// Done removes a swept tag.
	Done(ctx context.Context, tag string) error
	// Retry records a failed attempt and when to try again.
	Retry(ctx context.Context, tag string, next time.Time, lastError string) error
}

// Deleter deletes every liveness transaction with a tag (regula.Client).
type Deleter interface {
	DeleteLivenessByTag(ctx context.Context, tag string) error
}

// Sweeper deletes the due tags.
type Sweeper struct {
	Queue   Queue
	Deleter Deleter
}

// Sweep deletes every tag due at now, up to Batch, and returns how many it
// cleared. A failed delete is retried later with backoff; it only stops this
// sweep when the queue itself fails.
func (s Sweeper) Sweep(ctx context.Context, now time.Time) (int, error) {
	due, err := s.Queue.Due(ctx, now, Batch)
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, e := range due {
		if err := s.Deleter.DeleteLivenessByTag(ctx, e.Tag); err != nil {
			// The tag is the session's random reference, not personal data.
			slog.WarnContext(ctx, "regula sweep: delete tag", slog.String("tag", e.Tag), slog.Int("attempt", e.Attempts+1), slog.Any("error", err))
			if err := s.Queue.Retry(ctx, e.Tag, now.Add(backoff(e.Attempts+1)), err.Error()); err != nil {
				return cleared, err
			}
			continue
		}
		if err := s.Queue.Done(ctx, e.Tag); err != nil {
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
}

// backoff is the wait after the attempts-th failure.
func backoff(attempts int) time.Duration {
	d := MinBackoff
	for i := 1; i < attempts && d < MaxBackoff; i++ {
		d *= 2
	}
	return min(d, MaxBackoff)
}

// dedupPruneAt is how many remembered tags make Dedup forget the past-due ones.
const dedupPruneAt = 1024

// Dedup wraps a Queue so a tag queued again with the same due time costs no
// write: the app view that hands out the tag is polled every few seconds.
type Dedup struct {
	Queue
	mu     sync.Mutex
	queued map[string]time.Time
}

// NewDedup returns q with repeated identical Adds skipped.
func NewDedup(q Queue) *Dedup { return &Dedup{Queue: q, queued: map[string]time.Time{}} }

func (d *Dedup) Add(ctx context.Context, tag string, dueAt time.Time) error {
	d.mu.Lock()
	if due, ok := d.queued[tag]; ok && !dueAt.After(due) {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	if err := d.Queue.Add(ctx, tag, dueAt); err != nil {
		return err
	}
	d.mu.Lock()
	d.queued[tag] = dueAt
	if len(d.queued) > dedupPruneAt {
		// Past due, the sweep has it (or will retry it): forget it here.
		now := time.Now()
		for t, due := range d.queued {
			if due.Before(now) {
				delete(d.queued, t)
			}
		}
	}
	d.mu.Unlock()
	return nil
}

// Done forgets the tag, so a session queued again after its sweep is kept.
func (d *Dedup) Done(ctx context.Context, tag string) error {
	d.mu.Lock()
	delete(d.queued, tag)
	d.mu.Unlock()
	return d.Queue.Done(ctx, tag)
}
