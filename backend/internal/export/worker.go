package export

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// DefaultPollInterval is how often an idle worker looks for queued work. An
// export is a deliberate act with a human waiting on a status page, not a hot
// path, so the loop stays cheap.
const DefaultPollInterval = 5 * time.Second

// DefaultBundleTTL is how long a finished bundle stays downloadable. It carries
// every member's personal data, so it expires rather than accumulating.
const DefaultBundleTTL = 24 * time.Hour

// DefaultRunTimeout bounds one assembly. A bundle that cannot be built inside it
// fails visibly instead of holding the worker forever.
const DefaultRunTimeout = 15 * time.Minute

type jobStore interface {
	Claim(ctx context.Context) (Job, error)
	Complete(ctx context.Context, jobID, bundleID uuid.UUID, filename string, content []byte, expiresAt time.Time) (string, error)
	Fail(ctx context.Context, jobID uuid.UUID, reason string) error
}

type orgResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID) (Organization, error)
}

// ReadyHandler is told about a finished bundle and its raw download token — the
// one moment the token exists in the clear. It is how a termination export
// reaches an owner who can no longer sign in; an interactive request needs
// nothing here, because the requester reads the token from the job status.
type ReadyHandler func(ctx context.Context, job Job, token string)

// Worker assembles queued bundles. It runs one job at a time on purpose: a
// bundle is held whole in memory while it is written, so building several at
// once would multiply the largest org's footprint by the concurrency.
type Worker struct {
	jobs     jobStore
	orgs     orgResolver
	service  *Service
	interval time.Duration
	ttl      time.Duration
	timeout  time.Duration
	onReady  ReadyHandler
}

func NewWorker(jobs jobStore, orgs orgResolver, service *Service) *Worker {
	return &Worker{
		jobs:     jobs,
		orgs:     orgs,
		service:  service,
		interval: DefaultPollInterval,
		ttl:      DefaultBundleTTL,
		timeout:  DefaultRunTimeout,
	}
}

// OnReady registers what to do with a finished bundle's download token.
func (w *Worker) OnReady(handler ReadyHandler) { w.onReady = handler }

// Run drains the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Keep going while there is work: one tick should not leave a queue
			// that built up while the worker was busy.
			for w.runOnce(ctx) {
			}
		}
	}
}

// runOnce claims and assembles at most one job, reporting whether it found any.
func (w *Worker) runOnce(ctx context.Context) bool {
	job, err := w.jobs.Claim(ctx)
	if errors.Is(err, ErrJobNotFound) {
		return false
	}
	if err != nil {
		slog.ErrorContext(ctx, "export: claiming job", slog.String("error", err.Error()))
		return false
	}

	if err := w.assemble(ctx, job); err != nil {
		slog.ErrorContext(ctx, "export: assembling bundle",
			slog.String("jobId", job.ID.String()),
			slog.String("orgId", job.OrganizationID.String()),
			slog.String("error", err.Error()))
		if failErr := w.jobs.Fail(ctx, job.ID, err.Error()); failErr != nil {
			slog.ErrorContext(ctx, "export: recording job failure",
				slog.String("jobId", job.ID.String()), slog.String("error", failErr.Error()))
		}
	}
	return true
}

func (w *Worker) assemble(ctx context.Context, job Job) error {
	runCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	org, err := w.orgs.Resolve(runCtx, job.OrganizationID)
	if err != nil {
		return err
	}

	archive, err := w.service.build(runCtx, org, job.Sections)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := archive.Close(); closeErr != nil {
			slog.ErrorContext(ctx, "export: cleaning up bundle",
				slog.String("jobId", job.ID.String()), slog.String("error", closeErr.Error()))
		}
	}()

	content, err := archive.Bytes()
	if err != nil {
		return err
	}

	token, err := w.jobs.Complete(runCtx, job.ID, archive.BundleID, archive.Filename, content, time.Now().Add(w.ttl))
	if err != nil {
		return err
	}
	if w.onReady != nil {
		w.onReady(ctx, job, token)
	}
	return nil
}
