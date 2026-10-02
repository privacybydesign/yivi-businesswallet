package proofingengine

import (
	"context"
	"log/slog"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regulasweep"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// queueRegulaSweep queues sess's Regula tag, as the app receives it, for
// deletion regulasweep.Grace after the session's end: every liveness attempt
// the app runs carries the tag, so the sweep removes the ones never submitted
// too. A failure to queue is logged, not the app's problem.
func (s *Server) queueRegulaSweep(ctx context.Context, sess session.Session) {
	tag := regulaTag(sess)
	if s.cfg.RegulaSweeps == nil || tag == "" {
		return
	}
	if err := s.cfg.RegulaSweeps.Add(ctx, tag, sess.ExpiresAt.Add(regulasweep.Grace)); err != nil {
		slog.WarnContext(ctx, "identity proofing: queue Regula sweep", slog.String("session_id", sess.ID), slog.Any("error", err))
	}
}
