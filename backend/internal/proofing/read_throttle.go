package proofing

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// readThrottleSweepAt is the entry count at which allow drops lapsed entries,
// so the map stays bounded by the requests read within one interval.
const readThrottleSweepAt = 1024

// readThrottle lets one call per request through per interval, in process.
type readThrottle struct {
	mu    sync.Mutex
	every time.Duration
	last  map[uuid.UUID]time.Time
}

func newReadThrottle(every time.Duration) *readThrottle {
	return &readThrottle{every: every, last: map[uuid.UUID]time.Time{}}
}

// allow reports whether id may be checked now, and if so counts it as checked.
func (t *readThrottle) allow(id uuid.UUID, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if at, ok := t.last[id]; ok && now.Sub(at) < t.every {
		return false
	}
	if len(t.last) >= readThrottleSweepAt {
		for k, at := range t.last {
			if now.Sub(at) >= t.every {
				delete(t.last, k)
			}
		}
	}
	t.last[id] = now
	return true
}
