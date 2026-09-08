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
	// DefaultIdentityScheduleInterval is how often the scheduler sweeps every
	// org's members for a due reminder. Re-identification is a slow-moving,
	// day-granularity deadline (identity_due_at is months out), so daily is
	// generous, mirroring provisioning's hourly directory sweep for a
	// similarly slow-moving source.
	DefaultIdentityScheduleInterval = 24 * time.Hour
	identitySweepTimeout            = 5 * time.Minute
)

// identityMailer is the slice of email.Service the scheduler needs — the two
// member-facing sends a reminder can be.
type identityMailer interface {
	SendIdentityReminder(ctx context.Context, orgID uuid.UUID, to, orgName, reidentifyURL, dueDate string) error
	SendIdentityOverdue(ctx context.Context, orgID uuid.UUID, to, orgName, reidentifyURL, dueDate string) error
}

// IdentityScheduler runs the daily re-identification reminder sweep (#240 §6):
// for every organisation with a re-identification policy, find members due a
// reminder, mail them a fresh re-identification link, and record the send so a
// restart never double-sends.
type IdentityScheduler struct {
	store      *Store
	mailer     identityMailer
	appBaseURL string
}

func NewIdentityScheduler(store *Store, mailer identityMailer, appBaseURL string) *IdentityScheduler {
	return &IdentityScheduler{store: store, mailer: mailer, appBaseURL: strings.TrimRight(appBaseURL, "/")}
}

// Start runs one sweep per tick until ctx is cancelled. It returns immediately;
// the first sweep is one interval away; a restart does not immediately re-sweep
// (the day-granularity deadline makes that unnecessary), mirroring
// provisioning.Scheduler's rationale.
func (s *IdentityScheduler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultIdentityScheduleInterval
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
					slog.ErrorContext(ctx, "identity: reminder sweep failed", slog.String("error", err.Error()))
				}
			}
		}
	}()
}

// SweepAll runs one pass over every organisation. Organisations are swept one
// after another — daily volume here is member counts, not directory-API rate
// limits, so there is no reason to parallelise. Only listing organisations is a
// hard failure; one org's sweep failing is logged and the pass continues, so a
// stuck mailer at one org never blocks reminders for every other org.
func (s *IdentityScheduler) SweepAll(ctx context.Context) error {
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

func (s *IdentityScheduler) sweepOne(ctx context.Context, org Organization) {
	ctx, cancel := context.WithTimeout(ctx, identitySweepTimeout)
	defer cancel()

	settings, err := s.store.GetIdentitySettings(ctx, org.ID)
	if err != nil {
		slog.ErrorContext(ctx, "identity: read settings failed", slog.String("organizationId", org.ID.String()), slog.String("error", err.Error()))
		return
	}
	// No policy for either member type: nothing can be due, skip the query.
	if !settings.Configured || (settings.EmployeeIntervalMonths == nil && settings.ExternalIntervalMonths == nil) {
		return
	}

	candidates, err := s.store.IdentityReminderCandidates(ctx, org.ID, settings)
	if err != nil {
		slog.ErrorContext(ctx, "identity: list reminder candidates failed", slog.String("organizationId", org.ID.String()), slog.String("error", err.Error()))
		return
	}
	sent := 0
	for _, c := range candidates {
		if s.remind(ctx, org, c) {
			sent++
		}
	}
	if sent > 0 {
		slog.InfoContext(ctx, "identity: reminder sweep completed",
			slog.String("organizationId", org.ID.String()), slog.Int("sent", sent), slog.Int("candidates", len(candidates)))
	}
}

// remind mints a fresh re-identification link and sends the due-soon or overdue
// mail, then records the send. It reports whether the mail was sent (a
// delivery failure is logged, not retried — the next sweep tries again since
// the cadence tracking is only updated on success).
func (s *IdentityScheduler) remind(ctx context.Context, org Organization, c ReminderCandidate) bool {
	token, _, err := s.store.EnsureReverifyToken(ctx, org.ID, c.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "identity: mint reverify token failed",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}
	url := s.appBaseURL + "/reidentify/" + token
	dueDate := c.DueAt.Format("2006-01-02")

	if c.Overdue {
		err = s.mailer.SendIdentityOverdue(ctx, org.ID, c.Email, org.Name, url, dueDate)
	} else {
		err = s.mailer.SendIdentityReminder(ctx, org.ID, c.Email, org.Name, url, dueDate)
	}
	if err != nil {
		slog.WarnContext(ctx, "identity: reminder e-mail not sent",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}

	if err := s.store.RecordIdentityReminderSent(ctx, org.ID, c.UserID, c.Overdue); err != nil {
		slog.ErrorContext(ctx, "identity: record reminder sent failed",
			slog.String("organizationId", org.ID.String()), slog.String("userId", c.UserID.String()), slog.String("error", err.Error()))
		return false
	}
	return true
}
