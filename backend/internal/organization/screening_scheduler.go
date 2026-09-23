package organization

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultScreeningScheduleInterval mirrors DefaultIdentityScheduleInterval:
	// a VOG's expiry is a slow-moving, day-granularity deadline, so daily is
	// generous.
	DefaultScreeningScheduleInterval = 24 * time.Hour
	screeningSweepTimeout            = 5 * time.Minute
)

// screeningMailer is the slice of email.Service the scheduler needs.
type screeningMailer interface {
	SendVogReminder(ctx context.Context, orgID uuid.UUID, to, orgName, vogURL, dueDate string) error
	SendVogExpired(ctx context.Context, orgID uuid.UUID, to, orgName, vogURL, dueDate string) error
}

// ScreeningScheduler runs the daily VOG reminder sweep (#242 §6): for every
// organisation with a screening policy, find members whose VOG is expiring
// soon or has expired, mail them, and record the send so a restart never
// double-sends. Mirrors IdentityScheduler.
type ScreeningScheduler struct {
	store      *Store
	mailer     screeningMailer
	appBaseURL string
}

func NewScreeningScheduler(store *Store, mailer screeningMailer, appBaseURL string) *ScreeningScheduler {
	return &ScreeningScheduler{store: store, mailer: mailer, appBaseURL: strings.TrimRight(appBaseURL, "/")}
}

// Start runs one sweep per tick until ctx is cancelled. It returns immediately;
// the first sweep is one interval away, mirroring IdentityScheduler.Start.
func (s *ScreeningScheduler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultScreeningScheduleInterval
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SweepAll(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.ErrorContext(ctx, "screening: reminder sweep failed", slog.String("error", err.Error()))
				}
			}
		}
	}()
}

// SweepAll runs one pass over every organisation, mirroring
// IdentityScheduler.SweepAll: one org's sweep failing is logged and the pass
// continues.
func (s *ScreeningScheduler) SweepAll(ctx context.Context) error {
	orgs, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.sweepOne(ctx, org)
	}
	return nil
}

func (s *ScreeningScheduler) sweepOne(ctx context.Context, org Organization) {
	ctx, cancel := context.WithTimeout(ctx, screeningSweepTimeout)
	defer cancel()

	settings, err := s.store.GetScreeningSettings(ctx, org.ID)
	if err != nil {
		slog.ErrorContext(ctx, "screening: read settings failed", slog.String("organizationId", org.ID.String()), slog.String("error", err.Error()))
		return
	}
	if settings.RequiredFor == ScreeningRequiredForNobody {
		return
	}

	candidates, err := s.store.VogReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		slog.ErrorContext(ctx, "screening: list reminder candidates failed", slog.String("organizationId", org.ID.String()), slog.String("error", err.Error()))
		return
	}
	sent := 0
	for _, c := range candidates {
		if s.remind(ctx, org, c) {
			sent++
		}
	}
	if sent > 0 {
		slog.InfoContext(ctx, "screening: reminder sweep completed",
			slog.String("organizationId", org.ID.String()), slog.Int("sent", sent), slog.Int("candidates", len(candidates)))
	}
}

// remind mints a fresh VOG link and sends the expiring-soon or expired mail,
// then records the send. It reports whether the mail was sent - a delivery
// failure is logged, not retried, since the cadence tracking is only updated
// on success.
func (s *ScreeningScheduler) remind(ctx context.Context, org Organization, c VogReminderCandidate) bool {
	token, _, err := s.store.EnsureVogToken(ctx, org.ID, c.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "screening: mint vog token failed",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}
	url := s.appBaseURL + "/vog/" + token
	dueDate := c.DueAt.Format("2006-01-02")

	if c.Overdue {
		err = s.mailer.SendVogExpired(ctx, org.ID, c.Email, org.Name, url, dueDate)
	} else {
		err = s.mailer.SendVogReminder(ctx, org.ID, c.Email, org.Name, url, dueDate)
	}
	if err != nil {
		slog.WarnContext(ctx, "screening: reminder e-mail not sent",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}

	if err := s.store.RecordVogReminderSent(ctx, org.ID, c.UserID, c.Email, c.Overdue); err != nil {
		slog.ErrorContext(ctx, "screening: record reminder sent failed",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}
	return true
}
