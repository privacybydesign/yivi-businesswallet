package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxDueCheck caps how long a due run can go unnoticed by a replica.
const maxDueCheck = 5 * time.Minute

// Periodic is the one skeleton of a recurring background job: it owns the
// timing and the claim that makes one replica run it per interval, so a job
// only supplies its sweep.
type Periodic struct {
	// Name labels the job in logs and keys its row in periodic_job_runs.
	Name string
	// Interval is the time between two runs, across replicas and restarts.
	Interval time.Duration
	// Runs is where the last run is kept. With it a run is due Interval after
	// the last one whichever replica ran it, so a restart neither repeats a
	// recent run nor postpones a due one. Nil runs on every tick, unclaimed.
	Runs Querier
}

// Start runs job whenever it is due until ctx is cancelled, checking at once
// and then on a ticker. It returns immediately.
func (p Periodic) Start(ctx context.Context, job func(context.Context) error) {
	go func() {
		p.runIfDue(ctx, job)
		ticker := time.NewTicker(p.checkEvery())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.runIfDue(ctx, job)
			}
		}
	}()
}

func (p Periodic) checkEvery() time.Duration {
	if p.Runs == nil {
		return p.Interval
	}
	return min(p.Interval, maxDueCheck)
}

func (p Periodic) runIfDue(ctx context.Context, job func(context.Context) error) {
	due, err := p.claim(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "periodic job: claim failed", slog.String("job", p.Name), slog.Any("error", err))
		}
		return
	}
	if !due {
		return
	}
	if err := job(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.ErrorContext(ctx, "periodic job failed", slog.String("job", p.Name), slog.Any("error", err))
	}
}

// claim reports whether a run is due and, in the same statement, takes it, so
// of replicas checking together exactly one gets true.
func (p Periodic) claim(ctx context.Context) (bool, error) {
	if p.Runs == nil {
		return true, nil
	}
	const q = `INSERT INTO periodic_job_runs (name, last_run_at) VALUES ($1, now())
		ON CONFLICT (name) DO UPDATE SET last_run_at = now()
		WHERE periodic_job_runs.last_run_at <= now() - make_interval(secs => $2)
		RETURNING name`
	var name string
	err := p.Runs.QueryRow(ctx, q, p.Name, p.Interval.Seconds()).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim run: %w", err)
	}
	return true, nil
}
