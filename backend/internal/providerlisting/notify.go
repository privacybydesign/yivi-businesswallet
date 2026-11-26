package providerlisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type delistedMailer interface {
	SendProviderDelisted(ctx context.Context, orgID uuid.UUID, recipients []string, orgName string) error
}

type orgDirectory interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
	ListAdminEmails(ctx context.Context, orgID uuid.UUID) ([]string, error)
}

// MailNotifier mails an org's admins through the org's own SMTP settings. An org
// without them is told only in the app, by its wallet unit's revoke reason.
type MailNotifier struct {
	mail delistedMailer
	orgs orgDirectory
}

func NewMailNotifier(mail delistedMailer, orgs orgDirectory) *MailNotifier {
	return &MailNotifier{mail: mail, orgs: orgs}
}

func (n *MailNotifier) ProviderDelisted(ctx context.Context, orgID uuid.UUID) error {
	org, err := n.orgs.GetByID(ctx, orgID)
	if err != nil {
		return fmt.Errorf("providerlisting: org %s: %w", orgID, err)
	}
	admins, err := n.orgs.ListAdminEmails(ctx, orgID)
	if err != nil {
		return fmt.Errorf("providerlisting: admins of org %s: %w", orgID, err)
	}
	if len(admins) == 0 {
		return nil
	}
	err = n.mail.SendProviderDelisted(ctx, orgID, admins, org.Name)
	if errors.Is(err, email.ErrNotConfigured) {
		slog.WarnContext(ctx, "providerlisting: org has no SMTP settings; its admins are not mailed that the wallet was revoked",
			slog.String("org_id", orgID.String()))
		return nil
	}
	return err
}
