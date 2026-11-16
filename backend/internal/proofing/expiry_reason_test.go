package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

func TestExpiryReason(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	ended := &ipsSession{ExpiresAt: past}
	for name, tc := range map[string]struct {
		req  Request
		want ExpiryReason
	}{
		"a hosted link nobody started": {Request{Hosted: true, Status: StatusPending, LinkExpiresAt: past}, ExpiryNotStarted},
		"a session not finished":       {Request{Status: StatusInProgress, session: ended}, ExpiryNotFinished},
		"a review the engine ended":    {Request{Status: StatusNeedsReview, session: &ipsSession{ExpiresAt: now.Add(time.Hour), EndedAt: &past}}, ExpiryReviewEnded},
		"a session still running":      {Request{Status: StatusInProgress, session: &ipsSession{ExpiresAt: now.Add(time.Hour)}}, ""},
		"an approved one":              {Request{Status: StatusApproved, session: ended}, ""},
		"a cancelled one":              {Request{Status: StatusPending, CancelledAt: &past}, ""},
	} {
		if got := tc.req.ExpiryReason(now); got != tc.want {
			t.Errorf("%s: ExpiryReason = %q, want %q", name, got, tc.want)
		}
	}
}

func TestCancelledByOf(t *testing.T) {
	member, key, subject := CancelledByMember, CancelledByAPIKey, CancelledBySubject
	for name, tc := range map[string]struct {
		ctx  context.Context
		want *CancelledBy
	}{
		"a member":   {audit.ContextWithActor(context.Background(), audit.Actor{UserID: uuid.New()}), &member},
		"an API key": {audit.ContextWithActor(context.Background(), audit.Actor{Label: APIKeyActorPrefix + "yp_live_ab12"}), &key},
		"the person": {audit.ContextWithActor(context.Background(), hostedSubjectActor), &subject},
		"no actor":   {context.Background(), nil},
	} {
		got := cancelledByOf(tc.ctx)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: cancelledByOf = %v, want %v", name, got, tc.want)
		}
	}
}
