// Package emailchannel is the e-mail delivery route of the notification layer:
// it turns one dispatched event into a message to the organization's admins,
// sent through that org's own SMTP settings and rendered from its own mail
// template (internal/email), so a notification looks like every other mail the
// organization sends.
//
// It holds no message copy and no SMTP knowledge of its own. What it owns is the
// mapping from a recorded event onto the mail's variables: who is told (the org's
// admins), what the message says the event was (the catalogue's name for the
// audit action) and how much of the event's metadata it repeats (details.go).
package emailchannel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// auditLogPath is the app route the mail's call to action opens, under the org's
// slug. The full record of the event lives there, behind the same access control
// as every other org page.
const auditLogPath = "/audit-log"

// notificationMailer sends the rendered notification (implemented by
// *email.Service).
type notificationMailer interface {
	SendEventNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName string, n email.EventNotification) error
}

// orgDirectory resolves who to tell and what to call them (implemented by
// *organization.Store).
type orgDirectory interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
	ListAdminEmails(ctx context.Context, orgID uuid.UUID) ([]string, error)
}

// Channel delivers notifications by e-mail. Register it on the dispatcher at
// startup; a deployment that leaves it out keeps orgs' saved e-mail preferences
// and simply does not deliver them (see notifications.Dispatcher).
type Channel struct {
	mail       notificationMailer
	orgs       orgDirectory
	appBaseURL string
}

// New builds the channel. appBaseURL is the deployment's frontend base URL, which
// the mail's audit-log link is built on; config.Load has already checked it is an
// absolute http(s) URL, which is what the mail renderer requires of a link.
func New(mail notificationMailer, orgs orgDirectory, appBaseURL string) *Channel {
	return &Channel{mail: mail, orgs: orgs, appBaseURL: strings.TrimRight(appBaseURL, "/")}
}

func (c *Channel) ID() notifications.ChannelID { return notifications.ChannelEmail }

// Notify mails the organization's admins about one event. Two cases are not
// failures and return nil: an org with no admins has nobody to tell, and an org
// that subscribed to e-mail without configuring SMTP is a misconfiguration to
// warn about, not an error to log once per event at ERROR.
func (c *Channel) Notify(ctx context.Context, e notifications.Event) error {
	org, err := c.orgs.GetByID(ctx, e.OrgID)
	if err != nil {
		return fmt.Errorf("emailchannel: organization %s: %w", e.OrgID, err)
	}
	admins, err := c.orgs.ListAdminEmails(ctx, e.OrgID)
	if err != nil {
		return fmt.Errorf("emailchannel: admins of organization %s: %w", e.OrgID, err)
	}
	if len(admins) == 0 {
		return nil
	}

	err = c.mail.SendEventNotification(ctx, e.OrgID, admins, org.Name, email.EventNotification{
		Action:     e.Action,
		Details:    summarize(e.Metadata),
		OccurredAt: e.OccurredAt,
		AuditURL:   c.appBaseURL + "/" + org.Slug + auditLogPath,
	})
	switch {
	case errors.Is(err, email.ErrNotConfigured):
		slog.WarnContext(ctx, "notifications: e-mail is subscribed but SMTP is not configured",
			slog.String("organizationId", e.OrgID.String()),
			slog.String("action", e.Action))
		return nil
	case err != nil:
		return fmt.Errorf("emailchannel: notifying the admins of organization %s: %w", e.OrgID, err)
	}
	return nil
}
