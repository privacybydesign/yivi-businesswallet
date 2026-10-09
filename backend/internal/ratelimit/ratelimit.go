// Package ratelimit is an in-process, keyed token-bucket limiter: each key
// (an API key id, a token hash, a client IP) gets its own bucket of Burst
// tokens that refills at Rate. It imports no other internal/* package.
//
// The buckets live in this process only, so a deployment of N API replicas
// admits up to N times the limit. That bounds abuse by one caller; it is not a
// quota to bill against.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// sweepEvery is how many Allow calls pass between sweeps of idle buckets, so
// the map does not grow with every key ever seen.
const sweepEvery = 1024

// Limit is a bucket's shape: Burst tokens at most, refilled at one token per
// Per/Burst, so a caller that has been idle may send Burst at once and then
// Burst every Per.
type Limit struct {
	Burst int
	Per   time.Duration
}

// Limiter hands out tokens per key. The zero value is not usable; use New.
type Limiter struct {
	limit Limit
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

type bucket struct {
	tokens float64
	at     time.Time
}

// New returns a limiter of limit per key.
func New(limit Limit) *Limiter {
	return &Limiter{limit: limit, now: time.Now, buckets: map[string]*bucket{}}
}

// Allow takes one token from key's bucket. When the bucket is empty it takes
// nothing and returns false and how long until a token is back.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.calls++
	if l.calls%sweepEvery == 0 {
		l.sweep(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.limit.Burst), at: now}
		l.buckets[key] = b
	}
	b.tokens = l.refilled(b, now)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / l.perNanosecond()))
	return false, wait
}

// perNanosecond is the refill rate in tokens per nanosecond.
func (l *Limiter) perNanosecond() float64 {
	return float64(l.limit.Burst) / float64(l.limit.Per)
}

func (l *Limiter) refilled(b *bucket, now time.Time) float64 {
	return min(float64(l.limit.Burst), b.tokens+float64(now.Sub(b.at))*l.perNanosecond())
}

// sweep drops every bucket that has refilled completely: it is
// indistinguishable from a new one.
func (l *Limiter) sweep(now time.Time) {
	for key, b := range l.buckets {
		if l.refilled(b, now) >= float64(l.limit.Burst) {
			delete(l.buckets, key)
		}
	}
}
