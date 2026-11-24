package attestation

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// heldViewFreshness is how old an organization's last status re-check may be
// before opening its held credentials re-checks them first.
const heldViewFreshness = time.Minute

// heldViewWait is how long a page waits for that re-check; a slower one goes on
// in the background and the page shows what was known.
const heldViewWait = 3 * time.Second

// heldFreshness tracks, per organization, the last re-check a view started and
// the one still running, so concurrent views share one.
type heldFreshness struct {
	mu      sync.Mutex
	checked map[uuid.UUID]time.Time
	running map[uuid.UUID]chan struct{}
}

// refreshHeldIfStale re-checks the organization's held credentials when they
// were last re-checked longer than heldViewFreshness ago, so a credential the
// issuer revoked is not shown as valid until the scheduled re-check.
func (s *Service) refreshHeldIfStale(ctx context.Context, orgID uuid.UUID) {
	f := &s.heldFresh
	f.mu.Lock()
	done, ok := f.running[orgID]
	if !ok {
		if last, seen := f.checked[orgID]; seen && s.now().Sub(last) < heldViewFreshness {
			f.mu.Unlock()
			return
		}
		done = make(chan struct{})
		f.running[orgID] = done
		go s.runHeldRefresh(context.WithoutCancel(ctx), orgID, done)
	}
	f.mu.Unlock()

	wait := time.NewTimer(heldViewWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-wait.C:
	case <-ctx.Done():
	}
}

func (s *Service) runHeldRefresh(ctx context.Context, orgID uuid.UUID, done chan struct{}) {
	if _, err := s.RecheckHeld(ctx, orgID); err != nil {
		slog.WarnContext(ctx, "attestation: held status re-check on view failed",
			slog.String("orgId", orgID.String()), slog.Any("error", err))
	}
	f := &s.heldFresh
	f.mu.Lock()
	f.checked[orgID] = s.now()
	delete(f.running, orgID)
	f.mu.Unlock()
	close(done)
}
