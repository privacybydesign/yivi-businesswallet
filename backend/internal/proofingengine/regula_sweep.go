package proofingengine

import (
	"context"
	"log/slog"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regulasweep"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// queueRegulaSweep queues sess's Regula tag, as the app receives it, for
// deletion regulasweep.Grace after the session's end: every liveness attempt
// the app runs carries the tag, so the sweep removes the ones never submitted
// too. A session still running is due after its expiry; one that ended,
// however it ended, is due after that end, moving an earlier queueing forward.
// A failure to queue is logged, not the app's problem.
func (s *Server) queueRegulaSweep(ctx context.Context, sess session.Session) {
	tag := regulaTag(sess)
	if s.cfg.RegulaSweeps == nil || tag == "" {
		return
	}
	var err error
	if end, ended := sessionEnd(sess, time.Now().UTC()); ended {
		err = s.cfg.RegulaSweeps.Settle(ctx, tag, end.Add(regulasweep.Grace))
	} else {
		err = s.cfg.RegulaSweeps.Add(ctx, tag, sess.ExpiresAt.Add(regulasweep.Grace))
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: queue Regula sweep", slog.String("session_id", sess.ID), slog.Any("error", err))
	}
}

// sweepRegulaNow queues sess's Regula tag due now: an erased session's
// liveness transactions go with it, not a grace later. A failure to queue is
// logged; the erasure stands.
func (s *Server) sweepRegulaNow(ctx context.Context, sess session.Session) {
	tag := regulaTag(sess)
	if s.cfg.RegulaSweeps == nil || tag == "" {
		return
	}
	if err := s.cfg.RegulaSweeps.Settle(ctx, tag, time.Now().UTC()); err != nil {
		slog.WarnContext(ctx, "identity proofing: queue Regula sweep for an erased session",
			slog.String("session_id", sess.ID), slog.Any("error", err))
	}
}

// sessionEnd is when sess stopped taking evidence: its completion when it is
// decided, expired or cancelled, or now when it went to review (the app is
// done then too). ended is false for a session still running.
func sessionEnd(sess session.Session, now time.Time) (end time.Time, ended bool) {
	switch {
	case sess.Status.Terminal() && sess.CompletedAt != nil:
		return *sess.CompletedAt, true
	case sess.Status.Terminal(), sess.Status == session.StatusNeedsReview:
		return now, true
	}
	return time.Time{}, false
}
