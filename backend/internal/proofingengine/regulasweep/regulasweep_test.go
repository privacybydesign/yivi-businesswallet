package regulasweep

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
	"unicode/utf8"
)

// memQueue is Queue in memory.
type memQueue struct{ entries map[string]*Entry }

func (q *memQueue) Add(_ context.Context, tag string, dueAt time.Time) error {
	if e, ok := q.entries[tag]; ok {
		if dueAt.After(e.DueAt) {
			e.DueAt = dueAt
		}
		return nil
	}
	q.entries[tag] = &Entry{Tag: tag, DueAt: dueAt}
	return nil
}

func (q *memQueue) Settle(_ context.Context, tag string, dueAt time.Time) error {
	if e, ok := q.entries[tag]; ok {
		if dueAt.Before(e.DueAt) {
			e.DueAt = dueAt
		}
		return nil
	}
	q.entries[tag] = &Entry{Tag: tag, DueAt: dueAt}
	return nil
}

func (q *memQueue) Due(_ context.Context, now time.Time, limit int) ([]Entry, error) {
	var out []Entry
	for _, e := range q.entries {
		if !e.DueAt.After(now) {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (q *memQueue) Done(_ context.Context, tag string) error {
	delete(q.entries, tag)
	return nil
}

func (q *memQueue) Retry(_ context.Context, tag string, next time.Time, _ string) error {
	e := q.entries[tag]
	e.DueAt, e.Attempts = next, e.Attempts+1
	return nil
}

// fakeDeleter fails the tags in failing.
type fakeDeleter struct {
	deleted []string
	failing map[string]bool
}

func (d *fakeDeleter) DeleteLivenessByTag(_ context.Context, tag string) error {
	if d.failing[tag] {
		return errors.New("face api down")
	}
	d.deleted = append(d.deleted, tag)
	return nil
}

func TestSweepDeletesDueAndRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	q := &memQueue{entries: map[string]*Entry{}}
	_ = q.Add(ctx, "ips-due", now.Add(-time.Minute))
	_ = q.Add(ctx, "ips-failing", now.Add(-time.Minute))
	_ = q.Add(ctx, "ips-later", now.Add(time.Hour))
	d := &fakeDeleter{failing: map[string]bool{"ips-failing": true}}
	s := Sweeper{Queue: q, Deleter: d}

	cleared, err := s.Sweep(ctx, now)
	if err != nil || cleared != 1 {
		t.Fatalf("Sweep = %d, %v; want the one due tag cleared", cleared, err)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "ips-due" {
		t.Errorf("deleted %v, want only ips-due", d.deleted)
	}
	if _, ok := q.entries["ips-due"]; ok {
		t.Error("a swept tag stayed queued")
	}
	failing := q.entries["ips-failing"]
	if failing == nil || failing.Attempts != 1 || !failing.DueAt.Equal(now.Add(MinBackoff)) {
		t.Errorf("failed tag = %+v, want queued again a minute on with one attempt", failing)
	}
	if _, ok := q.entries["ips-later"]; !ok {
		t.Error("a tag not yet due was touched")
	}

	d.failing = nil
	if cleared, err := s.Sweep(ctx, now.Add(MinBackoff)); err != nil || cleared != 1 {
		t.Errorf("retry sweep = %d, %v; want the failed tag cleared once the Face API is back", cleared, err)
	}
}

func TestBackoffDoublesUpToAnHour(t *testing.T) {
	for attempts, want := range map[int]time.Duration{
		1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 7: time.Hour, 50: time.Hour,
	} {
		if got := backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %s, want %s", attempts, got, want)
		}
	}
}

// countingQueue counts the Adds that reach it.
type countingQueue struct {
	memQueue
	adds int
}

func (q *countingQueue) Add(ctx context.Context, tag string, dueAt time.Time) error {
	q.adds++
	return q.memQueue.Add(ctx, tag, dueAt)
}

func TestDedupSkipsRepeatedAdds(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	inner := &countingQueue{memQueue: memQueue{entries: map[string]*Entry{}}}
	d := NewDedup(inner)
	for range 5 {
		_ = d.Add(ctx, "ips-a", now)
	}
	if inner.adds != 1 {
		t.Errorf("five identical Adds reached the queue %d times, want 1", inner.adds)
	}
	_ = d.Add(ctx, "ips-a", now.Add(time.Hour))
	if inner.adds != 2 {
		t.Errorf("a later due time did not reach the queue (%d adds)", inner.adds)
	}
	_ = d.Done(ctx, "ips-a")
	_ = d.Add(ctx, "ips-a", now)
	if inner.adds != 3 {
		t.Errorf("a tag queued again after its sweep was skipped (%d adds)", inner.adds)
	}
}

// An error kept for a retry is valid UTF-8 within the bound, whatever the
// body was: cut on a character, and a non-UTF-8 body (Latin-1) repaired.
func TestTruncateUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"abc":          "abc",
		"ab€":          "ab",
		"caf\xe9 page": "caf",
	} {
		got := truncateUTF8(in, 4)
		if got != want || !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%q, 4) = %q, want %q", in, got, want)
		}
	}
}

// A session that ends moves its sweep forward to its end, and a later Add for
// the same tag (an app view read after the end) does not move it back through
// Settle; polling Settle with the same end costs one write.
func TestSettleSweepsFromTheEnd(t *testing.T) {
	ctx := context.Background()
	expiry := time.Now().Add(time.Hour)
	end := time.Now()
	inner := &countingQueue{memQueue: memQueue{entries: map[string]*Entry{}}}
	d := NewDedup(inner)
	if err := d.Add(ctx, "tag", expiry); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := d.Settle(ctx, "tag", end); err != nil {
			t.Fatal(err)
		}
	}
	if got := inner.entries["tag"].DueAt; !got.Equal(end) {
		t.Errorf("due at %v, want the session's end %v", got, end)
	}
	if err := d.Settle(ctx, "tag", expiry); err != nil {
		t.Fatal(err)
	}
	if got := inner.entries["tag"].DueAt; !got.Equal(end) {
		t.Errorf("a later settle moved the sweep to %v, want it kept at %v", got, end)
	}
}
