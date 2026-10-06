package proofing

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// SessionChannel is the Postgres channel that wakes ReconcileDue early.
const SessionChannel = "identity_proofing_sessions"

// deadlineRetry is how soon ReconcileDue looks again at a session whose cap
// passed while the engine still had it open.
const deadlineRetry = 30 * time.Second

// SessionChanged reconciles the request of a session the engine reports
// changed (it opened, a step started, or it settled): the engine calls it,
// off its request path, as the engine's event webhook used to.
func (s *Service) SessionChanged(ctx context.Context, sessionID string) {
	ctx = audit.WithoutActor(ctx)
	req, err := s.requests.GetBySession(ctx, sessionID)
	if errors.Is(err, ErrRequestNotFound) {
		// Created a moment ago and not attached yet; its deadline or the
		// next change reconciles it.
		return
	}
	if err == nil && req.needsReconcile() {
		s.reconcile(ctx, requestTenant(req), req)
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: session change not reconciled", slog.Any("error", err))
	}
}

// ReconcileDue ends every hosted link that lapsed unstarted, asks the engine about
// every session whose cap has passed without the wallet seeing it end, so its
// expiry (and webhook) lands on time, and returns the next deadline: a
// database.Job, woken early by SessionChannel.
func (s *Service) ReconcileDue(ctx context.Context) (time.Time, error) {
	ctx = audit.WithoutActor(ctx)
	now := s.now()
	lapsed, err := s.requests.LapseLinks(ctx, now, maxReconcilePerRound)
	if err != nil {
		return time.Time{}, err
	}
	if lapsed == maxReconcilePerRound {
		return now, nil
	}
	due, err := s.requests.ListDue(ctx, now, maxReconcilePerRound)
	if err != nil {
		return time.Time{}, err
	}
	open, settled := false, 0
	for _, req := range due {
		if s.reconcile(ctx, requestTenant(req), req).stillOpen() {
			open = true
		} else {
			settled++
		}
	}
	if len(due) == maxReconcilePerRound && settled > 0 {
		return now, nil
	}
	next, err := s.requests.NextDeadline(ctx, now)
	if err != nil {
		return time.Time{}, err
	}
	if retry := now.Add(deadlineRetry); open && (next.IsZero() || retry.Before(next)) {
		next = retry
	}
	return next, nil
}

func (r Request) stillOpen() bool {
	return (r.Status == StatusPending || r.Status == StatusInProgress) && r.session != nil && r.session.EndedAt == nil
}
