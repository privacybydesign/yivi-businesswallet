package emailchannel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// installTestLogger captures what the channel logs, which is the whole of what a
// tolerated misconfiguration produces.
func installTestLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	return func() { slog.SetDefault(prev) }
}

type sentMail struct {
	orgID        uuid.UUID
	recipients   []string
	orgName      string
	notification email.EventNotification
}

type recordingMailer struct {
	sent []sentMail
	err  error
}

func (r *recordingMailer) SendEventNotification(_ context.Context, orgID uuid.UUID, recipients []string, orgName string, n email.EventNotification) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, sentMail{orgID: orgID, recipients: recipients, orgName: orgName, notification: n})
	return nil
}

type stubDirectory struct {
	org       organization.Organization
	orgErr    error
	admins    []string
	adminsErr error
}

func (s stubDirectory) GetByID(context.Context, uuid.UUID) (organization.Organization, error) {
	return s.org, s.orgErr
}

func (s stubDirectory) ListAdminEmails(context.Context, uuid.UUID) ([]string, error) {
	return s.admins, s.adminsErr
}

func testOrg() organization.Organization {
	return organization.Organization{ID: uuid.New(), Name: "Acme BV", Slug: "acme"}
}

func testEvent(orgID uuid.UUID) notifications.Event {
	return notifications.Event{
		OrgID:      orgID,
		Action:     "membership.invited",
		TargetType: "membership",
		TargetID:   "sam@example.org",
		Metadata: map[string]any{"after": map[string]any{
			"email": "sam@example.org", "role": "member",
		}},
		OccurredAt: time.Date(2026, 1, 14, 9, 32, 0, 0, time.UTC),
	}
}

func TestNotifyMailsTheOrganizationAdmins(t *testing.T) {
	org := testOrg()
	mailer := &recordingMailer{}
	channel := New(mailer, stubDirectory{
		org:    org,
		admins: []string{"ada@acme.example", "sam@acme.example"},
	}, "https://wallet.example.org/")

	if err := channel.Notify(context.Background(), testEvent(org.ID)); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(mailer.sent))
	}
	sent := mailer.sent[0]
	if sent.orgID != org.ID {
		t.Errorf("orgID = %s, want %s", sent.orgID, org.ID)
	}
	if sent.orgName != "Acme BV" {
		t.Errorf("orgName = %q", sent.orgName)
	}
	if len(sent.recipients) != 2 || sent.recipients[0] != "ada@acme.example" {
		t.Errorf("recipients = %v, want both admins", sent.recipients)
	}
	if sent.notification.Action != "membership.invited" {
		t.Errorf("action = %q", sent.notification.Action)
	}
	if sent.notification.Details != "email: sam@example.org\nrole: member" {
		t.Errorf("details = %q", sent.notification.Details)
	}
	// A trailing slash on the configured base URL must not double up in the link.
	if sent.notification.AuditURL != "https://wallet.example.org/acme/audit-log" {
		t.Errorf("auditUrl = %q", sent.notification.AuditURL)
	}
	if !sent.notification.OccurredAt.Equal(time.Date(2026, 1, 14, 9, 32, 0, 0, time.UTC)) {
		t.Errorf("occurredAt = %s", sent.notification.OccurredAt)
	}
}

func TestNotifyReportsTheChannelID(t *testing.T) {
	channel := New(&recordingMailer{}, stubDirectory{}, "https://wallet.example.org")
	if channel.ID() != notifications.ChannelEmail {
		t.Errorf("ID() = %q, want %q", channel.ID(), notifications.ChannelEmail)
	}
}

// An org whose last admin just left has nobody to tell. That is not a delivery
// failure, so it must not be logged as one.
func TestNotifySendsNothingWithoutAdmins(t *testing.T) {
	org := testOrg()
	mailer := &recordingMailer{}
	channel := New(mailer, stubDirectory{org: org}, "https://wallet.example.org")

	if err := channel.Notify(context.Background(), testEvent(org.ID)); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("sent %d notifications, want none", len(mailer.sent))
	}
}

// Subscribing to e-mail without configuring SMTP is a misconfiguration the admin
// has to fix; every dispatch pass logging it at ERROR would not help them. One pass
// claims up to a hundred events, so the warning is also per org rather than per
// event: the same line a hundred times a tick is the noise readSubscriptions
// already documents itself as avoiding.
func TestNotifyToleratesUnconfiguredSMTP(t *testing.T) {
	org := testOrg()
	var logged bytes.Buffer
	restore := installTestLogger(&logged)
	defer restore()

	channel := New(&recordingMailer{err: email.ErrNotConfigured}, stubDirectory{
		org:    org,
		admins: []string{"ada@acme.example"},
	}, "https://wallet.example.org")

	for i := range 3 {
		if err := channel.Notify(context.Background(), testEvent(org.ID)); err != nil {
			t.Errorf("Notify %d = %v, want nil", i, err)
		}
	}

	if got := strings.Count(logged.String(), "SMTP is not configured"); got != 1 {
		t.Errorf("warned %d times over 3 events, want once:\n%s", got, logged.String())
	}
	if !strings.Contains(logged.String(), org.ID.String()) {
		t.Errorf("the warning does not name the organization:\n%s", logged.String())
	}
}

func TestNotifyReportsDeliveryAndLookupFailures(t *testing.T) {
	org := testOrg()
	failure := errors.New("database down")
	tests := map[string]struct {
		directory orgDirectory
		mailer    *recordingMailer
	}{
		"organization lookup": {
			directory: stubDirectory{orgErr: failure},
			mailer:    &recordingMailer{},
		},
		"admin lookup": {
			directory: stubDirectory{org: org, adminsErr: failure},
			mailer:    &recordingMailer{},
		},
		"delivery": {
			directory: stubDirectory{org: org, admins: []string{"ada@acme.example"}},
			mailer:    &recordingMailer{err: failure},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			channel := New(tc.mailer, tc.directory, "https://wallet.example.org")
			if err := channel.Notify(context.Background(), testEvent(org.ID)); !errors.Is(err, failure) {
				t.Errorf("Notify = %v, want %v", err, failure)
			}
		})
	}
}

// The mail names the event, so every event an org can subscribe to needs copy for
// it in every locale — and a label for an event that has left the catalog is dead
// copy. This is the only place both catalogues are in scope, so it is checked here.
func TestEverySubscribableEventHasMailCopy(t *testing.T) {
	subscribable := map[string]bool{}
	for _, entry := range notifications.Catalog() {
		subscribable[entry.Event] = true
		for _, locale := range email.Locales() {
			if _, ok := email.EventLabel(entry.Event, locale); !ok {
				t.Errorf("locale %q: no mail name for subscribable event %q", locale, entry.Event)
			}
		}
	}
	for _, action := range email.LabelledEvents() {
		if !subscribable[action] {
			t.Errorf("the mail catalogue names %q, which cannot be subscribed to", action)
		}
	}
}
