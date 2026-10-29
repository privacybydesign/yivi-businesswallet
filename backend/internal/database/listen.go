package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notify signals listeners on channel once the caller's transaction commits
// (at once outside one).
func Notify(ctx context.Context, q Querier, channel string) error {
	_, err := q.Exec(ctx, `SELECT pg_notify($1, '')`, channel)
	return err
}

// Job does its work and returns when it next needs to run on its own: the
// zero time when only a notification should wake it.
type Job func(ctx context.Context) (next time.Time, err error)

const (
	listenRetryMin = time.Second
	listenRetryMax = 5 * time.Minute
)

// RunOnNotify runs job at start, on every NOTIFY on channel, and at the
// deadline job returns, until ctx ends. A failed or panicking run is retried
// with backoff. Nothing runs on a fixed interval.
func RunOnNotify(ctx context.Context, pool *pgxpool.Pool, channel, name string, job Job) {
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go Listen(ctx, pool, channel, signal, func(string) { signal() })
	go func() {
		retry := listenRetryMin
		for {
			next, err := runJob(ctx, job)
			if err != nil {
				slog.ErrorContext(ctx, "notified job failed", slog.String("job", name), slog.String("error", err.Error()))
				next, retry = time.Now().Add(retry), min(retry*2, listenRetryMax)
			} else {
				retry = listenRetryMin
			}

			var timer *time.Timer
			var fire <-chan time.Time
			if !next.IsZero() {
				timer = time.NewTimer(max(time.Until(next), 0))
				fire = timer.C
			}

			select {
			case <-ctx.Done():
			case <-wake:
			case <-fire:
			}

			if timer != nil {
				timer.Stop()
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

// runJob runs job, turning a panic into an error: the job runs on its own
// goroutine, where a panic would take the whole process down.
func runJob(ctx context.Context, job Job) (next time.Time, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return job(ctx)
}

// Listen holds a dedicated connection LISTENing on channel until ctx ends,
// reconnecting with backoff, and calls notified with each NOTIFY's payload.
// Each time LISTEN holds it calls listening once, for anything missed before.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, listening func(), notified func(payload string)) {
	retry := listenRetryMin
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, channel, notified, func() { retry = listenRetryMin; listening() })
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "listen lost, reconnecting", slog.String("channel", channel), slog.Any("error", err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
		retry = min(retry*2, listenRetryMax)
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, notified func(payload string), connected func()) error {
	pooled, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// A LISTENing connection must not go back to the pool.
	conn := pooled.Hijack()
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	connected()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		notified(n.Payload)
	}
}
