package provisioning

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultSyncInterval is how often the scheduler re-reads every enabled
	// organisation's directory. A directory changes on HR's timescale, not on a
	// user's, and each pass is a full read of somebody else's API — hourly is
	// generous for the first and considerate of the second.
	DefaultSyncInterval = time.Hour
	// DefaultSyncTimeout bounds one organisation's sync. It is enforced from the
	// outside so a source that stops answering delays that organisation only, not
	// every organisation behind it in the pass.
	DefaultSyncTimeout = 5 * time.Minute
)

// enabledLister is the scheduler's read of which organisations to sync,
// implemented by *Store.
type enabledLister interface {
	ListEnabled(ctx context.Context) ([]uuid.UUID, error)
}

// Scheduler runs Service.Sync for every organisation with provisioning switched
// on.
type Scheduler struct {
	orgs    enabledLister
	sync    syncer
	timeout time.Duration
}

func NewScheduler(orgs enabledLister, sync syncer) *Scheduler {
	return &Scheduler{orgs: orgs, sync: sync, timeout: DefaultSyncTimeout}
}

// Start syncs every enabled organisation on each tick until ctx is cancelled. It
// returns immediately. The first pass is one interval away rather than at boot:
// a deploy restarts every replica at once, and a directory that was read an hour
// ago does not need re-reading because we redeployed.
func (s *Scheduler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SyncAll(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.ErrorContext(ctx, "provisioning: sync pass failed",
						slog.String("error", err.Error()))
				}
			}
		}
	}()
}

// SyncAll runs one pass. Organisations are synced one after another: a pass is
// hourly and a directory read is somebody else's rate limit, so there is nothing
// to gain from doing them at once. Only failing to list the organisations is an
// error — an organisation whose own sync fails is logged (and recorded on its
// settings row) and the pass continues, so one expired secret does not stop
// everybody else's sync.
func (s *Scheduler) SyncAll(ctx context.Context) error {
	orgIDs, err := s.orgs.ListEnabled(ctx)
	if err != nil {
		return err
	}
	for _, orgID := range orgIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.syncOne(ctx, orgID)
	}
	return nil
}

func (s *Scheduler) syncOne(ctx context.Context, orgID uuid.UUID) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	result, err := s.sync.Sync(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "provisioning: sync failed",
			slog.String("organizationId", orgID.String()),
			slog.String("error", err.Error()))
		return
	}
	slog.InfoContext(ctx, "provisioning: sync completed",
		slog.String("organizationId", orgID.String()),
		slog.Int("departmentsCreated", result.DepartmentsCreated),
		slog.Int("membersInvited", result.MembersInvited),
		slog.Int("membersUpdated", result.MembersUpdated),
		slog.Int("membersRemoved", result.MembersRemoved),
		slog.Int("skipped", len(result.Skipped)))
}
