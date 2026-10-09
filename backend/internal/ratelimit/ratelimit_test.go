package ratelimit

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newTestLimiter(limit Limit) (*Limiter, *clock) {
	c := &clock{t: time.Unix(0, 0)}
	l := New(limit)
	l.now = c.now
	return l, c
}

func TestAllowBurstThenRefill(t *testing.T) {
	l, c := newTestLimiter(Limit{Burst: 3, Per: 3 * time.Second})
	for i := range 3 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("call %d refused within the burst", i+1)
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait != time.Second {
		t.Fatalf("Allow past the burst = %v, %v; want refused, 1s", ok, wait)
	}
	c.advance(time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Error("a refilled token was refused")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Error("allowed more than refilled")
	}
}

func TestKeysHaveTheirOwnBucket(t *testing.T) {
	l, _ := newTestLimiter(Limit{Burst: 1, Per: time.Minute})
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first call for a refused")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("second call for a allowed")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("b refused for a's spending")
	}
}

func TestRefillNeverExceedsBurst(t *testing.T) {
	l, c := newTestLimiter(Limit{Burst: 2, Per: time.Second})
	l.Allow("k")
	c.advance(time.Hour)
	for range 2 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatal("refused within the burst after idling")
		}
	}
	if ok, _ := l.Allow("k"); ok {
		t.Error("idling banked more than the burst")
	}
}

func TestSweepDropsIdleBuckets(t *testing.T) {
	l, c := newTestLimiter(Limit{Burst: 1, Per: time.Second})
	l.Allow("idle")
	c.advance(time.Minute)
	for range sweepEvery {
		l.Allow("busy")
	}
	if _, ok := l.buckets["idle"]; ok {
		t.Error("an idle, refilled bucket survived the sweep")
	}
	if _, ok := l.buckets["busy"]; !ok {
		t.Error("a spent bucket was swept")
	}
}
