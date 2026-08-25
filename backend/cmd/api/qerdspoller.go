package main

import (
	"context"
	"log/slog"
	"time"
)

// inboundPoller is the QERDS service's sweep over every provisioned address.
type inboundPoller interface {
	PollAll(ctx context.Context) (int, error)
}

// startQerdsInboundPoller drains inbound QERDS messages for every provisioned
// address on a ticker, until ctx is cancelled.
//
// Without it, inbound intake only happens when an organization's console calls
// the org-scoped poll endpoint — i.e. when a human has a browser tab open. That
// is tolerable for messages an operator is waiting for, but not for
// machine-to-machine delivery: a remote party (e.g. ver.id over AS4) pushes a
// credential offer whose pre-authorized code is short-lived, so an offer that
// waits for someone to log in can expire before it is ever redeemed.
//
// Interval 0 disables it, restoring the console-only behaviour.
//
// Within one process this sweep and the console's org-scoped Poll cannot overlap:
// qerds.Service serialises inbound drains. They read the same access-point queue,
// and retrieveMessage acknowledges and consumes, so two concurrent drains can
// both see one message id and the loser's retrieve fails — on the console path
// that surfaced as a 500 on "check inbox".
//
// ACROSS processes it is still a single-replica assumption, same as the pruner:
// two replicas both polling race on that queue with nothing to serialise them.
// Intake is idempotent (dedupe on provider ref) so the outcome stays correct, but
// the work is wasted and one of the two retrieves fails. Multi-replica inbound is
// an open item in .ai/features/qerds.md.
func startQerdsInboundPoller(ctx context.Context, svc inboundPoller, every time.Duration) {
	if every <= 0 {
		slog.InfoContext(ctx, "qerds background inbound poller disabled",
			slog.String("reason", "interval is zero"))
		return
	}

	slog.InfoContext(ctx, "qerds background inbound poller started",
		slog.Duration("interval", every))

	ticker := time.NewTicker(every)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				received, err := svc.PollAll(ctx)
				if err != nil {
					// PollAll already logged the per-address detail and kept
					// sweeping; this is the summary line.
					slog.ErrorContext(ctx, "qerds background inbound poll had failures",
						slog.Int("received", received),
						slog.String("error", err.Error()))
					continue
				}
				if received > 0 {
					slog.InfoContext(ctx, "qerds background inbound poll received messages",
						slog.Int("received", received))
				}
			}
		}
	}()
}
