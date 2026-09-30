package database

import (
	"context"
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
// deadline job returns, until ctx ends. A failed run is retried with backoff.
// Nothing runs on a fixed interval.
func RunOnNotify(ctx context.Context, pool *pgxpool.Pool, channel, name string, job Job) {
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go listen(ctx, pool, channel, signal)
	go func() {
		retry := listenRetryMin
		for {
			next, err := job(ctx)
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

// listen holds a dedicated connection LISTENing on channel, reconnecting with
// backoff; each time LISTEN holds it signals once for anything missed before.
func listen(ctx context.Context, pool *pgxpool.Pool, channel string, signal func()) {
	retry := listenRetryMin
	for ctx.Err() == nil {
		// Once LISTEN holds, run again for anything committed before it did.
		err := listenOnce(ctx, pool, channel, signal, func() { retry = listenRetryMin; signal() })
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

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, signal, connected func()) error {
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
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		signal()
	}
}
